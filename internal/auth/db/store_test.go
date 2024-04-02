package db_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/willemschots/househunt/internal/auth"
	"github.com/willemschots/househunt/internal/auth/db"
	"github.com/willemschots/househunt/internal/db/testdb"
	"github.com/willemschots/househunt/internal/email"
	"github.com/willemschots/househunt/internal/errorz"
)

func Test_Tx_CreateUser(t *testing.T) {
	t.Run("ok, create user", inTx(func(t *testing.T, tx auth.Tx) {
		user := newUser(t, nil)

		err := tx.CreateUser(&user)
		if err != nil {
			t.Fatalf("failed to save user: %v", err)
		}

		want := newUser(t, func(u *auth.User) {
			// The store should set the following fields of user.
			u.ID = 1
			u.CreatedAt = now(t, 0)
			u.UpdatedAt = now(t, 0)
		})

		if !reflect.DeepEqual(user, want) {
			t.Errorf("got\n%#v\nwant\n%#v\n", user, want)
		}

		assertFindUser(t, tx, want)
	}))

	t.Run("fail, email constraint violated", inTx(func(t *testing.T, tx auth.Tx) {
		user1 := newUser(t, nil)
		err := tx.CreateUser(&user1)
		if err != nil {
			t.Fatalf("failed to save user: %v", err)
		}

		user2 := newUser(t, nil)
		err = tx.CreateUser(&user2)
		if !errors.Is(err, errorz.ErrConstraintViolated) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrConstraintViolated, err)
		}
	}))

	t.Run("fail, non zero ID", inTx(func(t *testing.T, tx auth.Tx) {
		user := newUser(t, func(u *auth.User) {
			u.ID = 1
		})

		err := tx.CreateUser(&user)
		if !errors.Is(err, errorz.ErrConstraintViolated) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrConstraintViolated, err)
		}
	}))
}

func Test_Tx_UpdateUser(t *testing.T) {
	setup := func(t *testing.T, tx auth.Tx) auth.User {
		user := newUser(t, nil)
		err := tx.CreateUser(&user)
		if err != nil {
			t.Fatalf("failed to save user: %v", err)
		}

		return user
	}

	t.Run("ok, update user", inTx(func(t *testing.T, tx auth.Tx) {
		user := setup(t, tx)

		// Update all fields that can be modified.
		user.Email = must(email.ParseAddress("jacob@example.com"))
		user.PasswordHash = must(auth.ParseArgon2Hash("$argon2id$v=19$m=47104,t=1,p=1$CkX5zzYLJMWm0y/17eScyw$Qfah+NewdsdeF0+iV72mShZhRO93Qwzdj17TUZCH6ZU"))
		user.IsActive = true

		err := tx.UpdateUser(&user)
		if err != nil {
			t.Fatalf("failed to save user: %v", err)
		}

		want := newUser(t, func(u *auth.User) {
			u.ID = 1
			u.Email = must(email.ParseAddress("jacob@example.com"))
			u.PasswordHash = must(auth.ParseArgon2Hash("$argon2id$v=19$m=47104,t=1,p=1$CkX5zzYLJMWm0y/17eScyw$Qfah+NewdsdeF0+iV72mShZhRO93Qwzdj17TUZCH6ZU"))
			u.IsActive = true
			u.CreatedAt = now(t, 0)
			u.UpdatedAt = now(t, 1) // The store should update the UpdatedAt field.
		})

		if !reflect.DeepEqual(user, want) {
			t.Errorf("got\n%#v\nwant\n%#v\n", user, want)
		}

		assertFindUser(t, tx, want)
	}))

	t.Run("fail, not found", inTx(func(t *testing.T, tx auth.Tx) {
		setup(t, tx)

		user2 := newUser(t, func(u *auth.User) {
			u.ID = 2
		})

		err := tx.UpdateUser(&user2)
		if !errors.Is(err, errorz.ErrNotFound) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrNotFound, err)
		}
	}))

	t.Run("fail, change email to an existing email", inTx(func(t *testing.T, tx auth.Tx) {
		user1 := setup(t, tx)

		user2 := newUser(t, func(u *auth.User) {
			u.Email = must(email.ParseAddress("jacob@example.com"))
		})

		err := tx.CreateUser(&user2)
		if err != nil {
			t.Fatalf("failed to save user: %v", err)
		}

		// Attempt to change user1's email to user2's email.
		user1.Email = user2.Email
		err = tx.UpdateUser(&user1)
		if !errors.Is(err, errorz.ErrConstraintViolated) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrConstraintViolated, err)
		}
	}))
}

func Test_Tx_FindUser(t *testing.T) {
	setupUsers := func(t *testing.T, tx auth.Tx) []auth.User {
		users := []auth.User{
			newUser(t, nil),
			newUser(t, func(u *auth.User) {
				u.Email = must(email.ParseAddress("jacob@example.com"))
				u.IsActive = true
			}),
			newUser(t, func(u *auth.User) {
				u.Email = must(email.ParseAddress("eva@example.com"))
			}),
		}

		for i := range users {
			err := tx.CreateUser(&users[i])
			if err != nil {
				t.Fatalf("failed to save user: %v", err)
			}
		}

		return users
	}

	tests := map[string]struct {
		filter   *auth.UserFilter
		wantFunc func([]auth.User) []auth.User
	}{
		"ok, all users, empty slices": {
			filter: &auth.UserFilter{
				IDs:      []int{},
				Emails:   []email.Address{},
				IsActive: nil,
			},
			wantFunc: func(users []auth.User) []auth.User {
				return users
			},
		},
		"ok, active users": {
			filter: &auth.UserFilter{
				IsActive: ptr(true),
			},
			wantFunc: func(users []auth.User) []auth.User {
				return users[1:2]
			},
		},
		"ok, one by id": {
			filter: &auth.UserFilter{
				IDs: []int{2},
			},
			wantFunc: func(users []auth.User) []auth.User {
				return []auth.User{users[1]}
			},
		},
		"ok, several by id": {
			filter: &auth.UserFilter{
				IDs: []int{1, 3},
			},
			wantFunc: func(users []auth.User) []auth.User {
				return []auth.User{
					users[0], users[2],
				}
			},
		},
		"ok, one by email": {
			filter: &auth.UserFilter{
				Emails: []email.Address{
					must(email.ParseAddress("jacob@example.com")),
				},
			},
			wantFunc: func(users []auth.User) []auth.User {
				return []auth.User{users[1]}
			},
		},
		"ok, several by email": {
			filter: &auth.UserFilter{
				Emails: []email.Address{
					must(email.ParseAddress("jacob@example.com")),
					must(email.ParseAddress("eva@example.com")),
				},
			},
			wantFunc: func(users []auth.User) []auth.User {
				return []auth.User{
					users[1], users[2],
				}
			},
		},
		"ok, combine filters": {
			filter: &auth.UserFilter{
				IDs: []int{1, 3},
				Emails: []email.Address{
					must(email.ParseAddress("alice@example.com")),
				},
				IsActive: ptr(false),
			},
			wantFunc: func(users []auth.User) []auth.User {
				return users[0:1]
			},
		},
		"ok, no results": {
			filter: &auth.UserFilter{
				IDs: []int{4},
			},
			wantFunc: func(users []auth.User) []auth.User {
				return []auth.User{}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			store := storeForTest(t)

			tx, err := store.BeginTx(context.Background())
			if err != nil {
				t.Fatalf("failed to begin tx: %v", err)
			}

			users := setupUsers(t, tx)
			want := tc.wantFunc(users)

			got, err := tx.FindUsers(tc.filter)
			if err != nil {
				t.Fatalf("failed to find users: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("got\n%#v\nwant\n%#v\n", got, want)
			}
		})
	}
}

func Test_Tx_CreateEmailToken(t *testing.T) {
	setup := func(t *testing.T, tx auth.Tx) auth.User {
		user := newUser(t, nil)
		err := tx.CreateUser(&user)
		if err != nil {
			t.Fatalf("failed to save user: %v", err)
		}

		return user
	}

	t.Run("ok, create email token", inTx(func(t *testing.T, tx auth.Tx) {
		_ = setup(t, tx)

		token := newEmailToken(t, nil)

		err := tx.CreateEmailToken(&token)
		if err != nil {
			t.Fatalf("failed to save email token: %v", err)
		}

		want := newEmailToken(t, func(tok *auth.EmailToken) {
			tok.ID = 1
			tok.CreatedAt = now(t, 1)
		})

		if !reflect.DeepEqual(token, want) {
			t.Fatalf("got\n%#v\nwant\n%#v\n", token, want)
		}

		assertFindEmailToken(t, tx, want)
	}))

	t.Run("fail, user foreign key does not exist", inTx(func(t *testing.T, tx auth.Tx) {
		setup(t, tx)

		token := newEmailToken(t, func(tok *auth.EmailToken) {
			tok.UserID = 101
		})

		err := tx.CreateEmailToken(&token)
		if !errors.Is(err, errorz.ErrConstraintViolated) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrConstraintViolated, err)
		}
	}))

	t.Run("fail, non zero ID", inTx(func(t *testing.T, tx auth.Tx) {
		setup(t, tx)

		token := newEmailToken(t, func(tok *auth.EmailToken) {
			tok.ID = 1
		})

		err := tx.CreateEmailToken(&token)
		if !errors.Is(err, errorz.ErrConstraintViolated) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrConstraintViolated, err)
		}
	}))
}

func Test_Tx_UpdateEmailToken(t *testing.T) {
	setup := func(t *testing.T, tx auth.Tx) (auth.User, auth.EmailToken) {
		user := newUser(t, nil)
		err := tx.CreateUser(&user)
		if err != nil {
			t.Fatalf("failed to save user: %v", err)
		}

		token := newEmailToken(t, nil)
		err = tx.CreateEmailToken(&token)
		if err != nil {
			t.Fatalf("failed to save email token: %v", err)
		}

		return user, token
	}

	t.Run("ok, update email token", inTx(func(t *testing.T, tx auth.Tx) {
		_, token := setup(t, tx)

		consumedAt := now(t, 9)
		token.ConsumedAt = &consumedAt

		err := tx.UpdateEmailToken(&token)
		if err != nil {
			t.Fatalf("failed to save email token: %v", err)
		}

		want := newEmailToken(t, func(tok *auth.EmailToken) {
			tok.ID = 1
			tok.CreatedAt = now(t, 1)
			consumedAtOther := now(t, 9)
			tok.ConsumedAt = &consumedAtOther // use different pointers.
		})

		assertFindEmailToken(t, tx, want)
	}))

	immutableFields := map[string]func(*auth.EmailToken, auth.User){
		"TokenHash": func(tok *auth.EmailToken, _ auth.User) {
			tok.TokenHash = must(auth.ParseArgon2Hash("$argon2id$v=19$m=47104,t=1,p=1$vP9U4C5jsOzFQLj0gvUkYw$YLrSb2dGfcVohlm8syynqHs6/NHxXS9rt/t6TjL7pi0"))
		},
		"UserID": func(tok *auth.EmailToken, user2 auth.User) {
			tok.UserID = user2.ID
		},
		"Email": func(tok *auth.EmailToken, _ auth.User) {
			tok.Email = must(email.ParseAddress("jacob@example.com"))
		},
		"Purpose": func(tok *auth.EmailToken, _ auth.User) {
			tok.Purpose = "other" // TODO: use a constant once we have one.
		},
	}

	for field, modFunc := range immutableFields {
		t.Run(fmt.Sprintf("fail, immutable field %s", field), inTxBadState(func(t *testing.T, tx auth.Tx) {
			_, token := setup(t, tx)

			// Create second user so we don't error on foreign key constraint.
			user2 := newUser(t, func(u *auth.User) {
				u.Email = "jacob@example.com"
			})
			err := tx.CreateUser(&user2)
			if err != nil {
				t.Fatalf("failed to save user: %v", err)
			}

			modFunc(&token, user2)

			err = tx.UpdateEmailToken(&token)
			if !errors.Is(err, errorz.ErrConstraintViolated) {
				t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrConstraintViolated, err)
			}
		}))
	}

	t.Run("fail, not found", inTx(func(t *testing.T, tx auth.Tx) {
		_, token := setup(t, tx)

		token.ID = 2
		err := tx.UpdateEmailToken(&token)
		if !errors.Is(err, errorz.ErrNotFound) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrNotFound, err)
		}
	}))
}

func Test_Tx_FindEmailTokenByID(t *testing.T) {
	// success cases already tested in Test_Tx_CreateEmailToken.

	t.Run("fail, not found", func(t *testing.T) {
		store := storeForTest(t)

		tx, err := store.BeginTx(context.Background())
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}

		_, err = tx.FindEmailTokenByID(2)
		if !errors.Is(err, errorz.ErrNotFound) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrNotFound, err)
		}
	})
}

func inTx(f func(*testing.T, auth.Tx)) func(*testing.T) {
	return func(t *testing.T) {
		store := storeForTest(t)

		tx, err := store.BeginTx(context.Background())
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}

		f(t, tx)

		err = tx.Commit()
		if err != nil {
			t.Fatalf("failed to commit tx: %v", err)
		}
	}
}

func inTxBadState(f func(*testing.T, auth.Tx)) func(t *testing.T) {
	return func(t *testing.T) {
		store := storeForTest(t)

		tx, err := store.BeginTx(context.Background())
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}

		f(t, tx)

		err = tx.Commit()
		if !errors.Is(err, errorz.ErrTxBadState) {
			t.Fatalf("expected errors to be %v got %v (via errors.Is)", errorz.ErrTxBadState, err)
		}
	}
}

func now(t *testing.T, i int) time.Time {
	t.Helper()

	if i > 9 {
		t.Fatalf("invalid time index: %d", i)
	}

	ts, err := time.Parse(time.RFC3339, fmt.Sprintf("2021-01-01T00:00:0%dZ", i))
	if err != nil {
		t.Fatalf("failed to parse time: %v", err)
	}

	return ts
}

func storeForTest(t *testing.T) *db.Store {
	t.Helper()

	testDB := testdb.RunWhile(t, true)

	i := 0
	return db.New(testDB, func() time.Time {
		n := now(t, i)
		i++
		return n
	})
}

func newUser(t *testing.T, modFunc func(*auth.User)) auth.User {
	t.Helper()

	u := auth.User{
		ID:           0,
		Email:        must(email.ParseAddress("alice@example.com")),
		PasswordHash: must(auth.ParseArgon2Hash("$argon2id$v=19$m=47104,t=1,p=1$vP9U4C5jsOzFQLj0gvUkYw$YLrSb2dGfcVohlm8syynqHs6/NHxXS9rt/t6TjL7pi0")),
		CreatedAt:    time.Time{},
		UpdatedAt:    time.Time{},
	}

	if modFunc != nil {
		modFunc(&u)
	}

	return u
}

func newEmailToken(t *testing.T, modFunc func(*auth.EmailToken)) auth.EmailToken {
	t.Helper()

	tok := auth.EmailToken{
		TokenHash:  must(auth.ParseArgon2Hash("$argon2id$v=19$m=47104,t=1,p=1$CkX5zzYLJMWm0y/17eScyw$Qfah+NewdsdeF0+iV72mShZhRO93Qwzdj17TUZCH6ZU")),
		UserID:     1,
		Email:      must(email.ParseAddress("alice@example.com")),
		Purpose:    auth.TokenPurposeActivate,
		CreatedAt:  time.Time{},
		ConsumedAt: nil,
	}

	if modFunc != nil {
		modFunc(&tok)
	}

	return tok
}

func assertFindUser(t *testing.T, tx auth.Tx, want auth.User) {
	t.Helper()

	got, err := tx.FindUsers(&auth.UserFilter{IDs: []int{want.ID}})
	if err != nil {
		t.Fatalf("failed to find user: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 user, got %d", len(got))
	}

	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("got\n%#v\nwant\n%#v\n", got[0], want)
	}
}

func assertFindEmailToken(t *testing.T, tx auth.Tx, want auth.EmailToken) {
	t.Helper()

	got, err := tx.FindEmailTokenByID(want.ID)
	if err != nil {
		t.Fatalf("failed to find email token: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%#v\nwant\n%#v\n", got, want)
	}
}

func ptr[T any](v T) *T {
	return &v
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
