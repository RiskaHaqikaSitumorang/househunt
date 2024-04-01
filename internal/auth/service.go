package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/willemschots/househunt/internal/email"
	"github.com/willemschots/househunt/internal/errorz"
)

var (
	ErrDuplicateAccount = errors.New("duplicate account")
)

// Store provides access to the user store.
type Store interface {
	BeginTx(ctx context.Context) (Tx, error)
}

// Tx is a transaction. If an error occurs on any of the Create/Update/Find methods,
// the transaction is considered to have failed and should be rolled back.
// Tx is not safe for concurrent use.
type Tx interface {
	Commit() error
	Rollback() error

	CreateUser(u *User) error
	UpdateUser(u *User) error
	FindUserByEmail(v email.Address) (User, error)

	CreateEmailToken(t *EmailToken) error
	UpdateEmailToken(t *EmailToken) error
	FindEmailTokenByID(id int) (EmailToken, error)
}

// ErrFunc is a function that handles errors.
type ErrFunc func(error)

// Service is the type that provides the main rules for
// authentication.
type Service struct {
	store       Store
	emailSvc    *email.Service
	wg          *sync.WaitGroup
	errHandler  ErrFunc
	workTimeout time.Duration // amount of time the "worker" goroutines are allowed to run.
}

func NewService(s Store, emailSvc *email.Service, errHandler ErrFunc, workTimeout time.Duration) *Service {
	svc := &Service{
		store:       s,
		emailSvc:    emailSvc,
		wg:          &sync.WaitGroup{},
		errHandler:  errHandler,
		workTimeout: workTimeout,
	}

	return svc
}

func (s *Service) Close() {
	s.wg.Wait()
}

// RegisterAccount registers a new account for the provided credentials.
func (s *Service) RegisterAccount(ctx context.Context, c Credentials) error {
	// Hash the password.
	pwdHash, err := c.Password.Hash()
	if err != nil {
		return err
	}

	user := User{
		Email:        c.Email,
		PasswordHash: pwdHash,
		IsActive:     false,
	}

	// The actual work is done in a separate goroutine to prevent:
	// - Waiting for the email to be send might slow down sending a response.
	// - Information leakage. Timing difference between existing/non-existing
	//   accounts could lead to account enumeration attacks.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		wCtx, cancel := context.WithTimeout(ctx, s.workTimeout)
		defer cancel()

		err := s.startActivation(wCtx, user)
		if err != nil {
			s.errHandler(err)
		}
	}()

	// Note that we don't let the caller know if the account was created or not.
	// This is by design, again to prevent information leakage.
	return nil
}

// startActivation begins the activation process of a new account:
// - Create a new user if necessary.
// - Create a new email token.
// - Send an email to address with an activation link.
//
// If an active user with the same email address exists, ErrDuplicateAccount is returned.
func (s *Service) startActivation(ctx context.Context, user User) error {
	token, err := GenerateToken()
	if err != nil {
		return err
	}

	tokenHash, err := token.Hash()
	if err != nil {
		return err
	}

	emailToken := EmailToken{
		TokenHash:  tokenHash,
		UserID:     0, // set after inserting the user.
		Email:      user.Email,
		Purpose:    TokenPurposeActivate,
		ConsumedAt: nil,
	}

	err = s.inTx(ctx, func(tx Tx) error {
		// TODO: Limit nr of tokens per user.

		_, txErr := tx.FindUserByEmail(user.Email)
		if txErr == nil {
			// TODO: Check if the user is active.
			// Only return an error if the user is active.
			return ErrDuplicateAccount
		}

		if !errors.Is(txErr, errorz.ErrNotFound) {
			return txErr
		}

		txErr = tx.CreateUser(&user)
		if txErr != nil {
			return txErr
		}

		emailToken.UserID = user.ID

		txErr = tx.CreateEmailToken(&emailToken)
		if txErr != nil {
			return txErr
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Send the email.
	// This could fail independently of the transaction. For now, this is an acceptable
	// risk. If the user has not received the email, they can always try to register again.
	//
	// If at some point this becomes unacceptable, we need to consider some kind of outbox
	// pattern.
	err = s.emailSvc.Send(ctx, "account-activation", user.Email, struct {
		ID    int
		Token Token
	}{
		ID:    emailToken.ID,
		Token: token,
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) inTx(ctx context.Context, f func(tx Tx) error) error {
	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		return err
	}

	err = f(tx)
	if err != nil {
		rBackErr := tx.Rollback()
		if rBackErr != nil {
			err = errors.Join(err, rBackErr)
		}
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}
