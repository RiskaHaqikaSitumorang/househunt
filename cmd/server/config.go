package main

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/willemschots/househunt/internal/auth"
	"github.com/willemschots/househunt/internal/email"
	"github.com/willemschots/househunt/internal/krypto"
	"github.com/willemschots/househunt/internal/web"
)

// httpConfig is the configuration for the HTTP server.
type httpConfig struct {
	addr            string
	readTimeout     time.Duration
	writeTimeout    time.Duration
	idleTimeout     time.Duration
	shutdownTimeout time.Duration
	// cookieKeys are the pairs of keys used to authenticate and encrypt cookies.
	// see https://pkg.go.dev/github.com/gorilla/sessions for more information on how these
	// are interpreted.
	cookieKeys   []krypto.Key
	secureCookie bool
	server       web.ServerConfig
}

// dbConfig is the database configuration.
type dbConfig struct {
	file           string
	migrate        bool
	encryptionKeys []krypto.Key
	blindIndexSalt krypto.Key
}

// config is the configuration for the server command.
type config struct {
	http  httpConfig
	db    dbConfig
	auth  auth.ServiceConfig
	email email.ServiceConfig
}

// defaultConfig returns a config with sane default values.
func defaultConfig() config {
	baseURL, err := url.Parse("http://localhost:8888")
	if err != nil {
		panic(fmt.Sprintf("failed to parse default base URL: %v", err))
	}

	return config{
		http: httpConfig{
			addr:            ":8888",
			readTimeout:     time.Second * 5,
			writeTimeout:    time.Second * 10,
			idleTimeout:     time.Second * 120,
			shutdownTimeout: time.Second * 15,
			secureCookie:    true,
		},
		db: dbConfig{
			file:    "househunt.db",
			migrate: true,
		},
		auth: auth.ServiceConfig{
			WorkerTimeout: time.Second * 30,
			TokenExpiry:   time.Minute * 30,
		},
		email: email.ServiceConfig{
			BaseURL: baseURL,
		},
	}
}

type envVariable struct {
	// required indicates if that an env variable must be provided by the user.
	required bool
	// mapFunc maps the env variable value to the config struct.
	mapFunc func(v string, c *config) error
}

// envMap maps environment variable names to fields in the config struct.
var envMap = map[string]envVariable{
	"BASE_URL": {
		mapFunc: func(v string, c *config) error {
			return confURL(v, c.email.BaseURL)
		},
	},
	"HTTP_ADDR": {
		mapFunc: func(v string, c *config) error {
			c.http.addr = v
			return nil
		},
	},
	"HTTP_READ_TIMEOUT": {
		mapFunc: func(v string, c *config) error {
			return confDuration(v, &c.http.readTimeout, 0, math.MaxInt64)
		},
	},
	"HTTP_WRITE_TIMEOUT": {
		mapFunc: func(v string, c *config) error {
			return confDuration(v, &c.http.writeTimeout, 0, math.MaxInt64)
		},
	},
	"HTTP_IDLE_TIMEOUT": {
		mapFunc: func(v string, c *config) error {
			return confDuration(v, &c.http.idleTimeout, 0, math.MaxInt64)
		},
	},
	"HTTP_SHUTDOWN_TIMEOUT": {
		mapFunc: func(v string, c *config) error {
			return confDuration(v, &c.http.shutdownTimeout, 0, math.MaxInt64)
		},
	},
	"HTTP_COOKIE_KEYS": {
		required: true,
		mapFunc: func(v string, c *config) error {
			return confSliceOf(v, &c.http.cookieKeys, krypto.ParseKey, 2, math.MaxInt64)
		},
	},
	"HTTP_SECURE_COOKIE": {
		mapFunc: func(v string, c *config) error {
			return confBool(v, &c.http.secureCookie)
		},
	},
	"HTTP_CSRF_KEY": {
		required: true,
		mapFunc: func(v string, c *config) error {
			return confCryptoKey(v, &c.http.server.CSRFKey)
		},
	},
	"DB_FILENAME": {
		mapFunc: func(v string, c *config) error {
			return confString(v, &c.db.file, 1, math.MaxInt64)
		},
	},
	"DB_MIGRATE": {
		mapFunc: func(v string, c *config) error {
			return confBool(v, &c.db.migrate)
		},
	},
	"DB_BLIND_INDEX_SALT": {
		required: true,
		mapFunc: func(v string, c *config) error {
			return confCryptoKey(v, &c.db.blindIndexSalt)
		},
	},
	"DB_ENCRYPTION_KEYS": {
		required: true,
		mapFunc: func(v string, c *config) error {
			return confSliceOf(v, &c.db.encryptionKeys, krypto.ParseKey, 2, math.MaxInt64)
		},
	},
	"AUTH_WORKER_TIMEOUT": {
		mapFunc: func(v string, c *config) error {
			return confDuration(v, &c.auth.WorkerTimeout, 0, math.MaxInt64)
		},
	},
	"AUTH_TOKEN_EXPIRY": {
		mapFunc: func(v string, c *config) error {
			return confDuration(v, &c.auth.TokenExpiry, 0, math.MaxInt64)
		},
	},
	"EMAIL_FROM": {
		required: true,
		mapFunc: func(v string, c *config) error {
			return confEmailAddress(v, &c.email.From)
		},
	},
}

// configFromEnv returns a config with values from the environment. It falls
// back to default values for any missing environment variables.
//
// It does a best effort to validate provided values, so that mistakes are
// caught ASAP. However, there is no guarantee that the returned config
// is valid and will work.
func configFromEnv() (config, error) {
	c := defaultConfig()

	var errSum error
	for key, envVar := range envMap {
		val, ok := os.LookupEnv(key)
		if !ok {
			if envVar.required {
				errSum = errors.Join(errSum, fmt.Errorf("missing required env variable %s", key))
			}
			continue
		}

		if err := envVar.mapFunc(val, &c); err != nil {
			errSum = errors.Join(errSum, fmt.Errorf("invalid env variable %s: %w", key, err))
		}
	}

	return c, errSum
}

// confDuration attempts to parse v into tgt as an URL.
func confURL(v string, tgt *url.URL) error {
	u, err := url.Parse(v)
	if err != nil {
		return err
	}

	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("URL %s is missing scheme or host", v)
	}

	*tgt = *u

	return nil
}

// confDuration attempts to parse v into tgt and checks if the result is in
// the provided range (inclusive).
func confDuration(v string, tgt *time.Duration, min, max time.Duration) error {
	dur, err := time.ParseDuration(v)
	if err != nil {
		return err
	}

	if dur < min || dur > max {
		return fmt.Errorf("duration %s not in range [%s, %s] (inclusive)", dur, min, max)
	}

	*tgt = dur

	return nil
}

func confString(v string, tgt *string, minLen, maxLen int) error {
	if len(v) < minLen || len(v) > maxLen {
		return fmt.Errorf("string length %d not in range [%d, %d] (inclusive)", len(v), minLen, maxLen)
	}

	*tgt = v

	return nil
}

// confDuration attempts to parse v into tgt as a bool.
func confBool(v string, tgt *bool) error {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return err
	}

	*tgt = b

	return nil
}

// confDuration attempts to parse v into tgt as a crypto key.
func confCryptoKey(v string, tgt *krypto.Key) error {
	k, err := krypto.ParseKey(v)
	if err != nil {
		return err
	}

	*tgt = k

	return nil
}

// confDuration attempts to parse v into tgt as an email address.
func confEmailAddress(v string, tgt *email.Address) error {
	email, err := email.ParseAddress(v)
	if err != nil {
		return err
	}

	*tgt = email

	return nil
}

func confSliceOf[T any](v string, tgt *[]T, elemFunc func(string) (T, error), minLen, maxLen int) error {
	for i, elem := range strings.Split(v, ",") {
		parsed, err := elemFunc(elem)
		if err != nil {
			return fmt.Errorf("failed to parse element %d: %w", i, err)
		}

		*tgt = append(*tgt, parsed)
	}

	if len(v) < minLen || len(v) > maxLen {
		return fmt.Errorf("slice length %d not in range [%d, %d] (inclusive)", len(v), minLen, maxLen)
	}

	return nil
}
