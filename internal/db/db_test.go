package db

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/config"
)

func TestSQLiteConcurrentWrites(t *testing.T) {
	for _, uri := range []string{
		filepath.Join(t.TempDir(), "bare.db"),
		"file:" + filepath.Join(t.TempDir(), "dsn.db") + "?cache=private",
	} {
		sqlDB, err := Open(config.DBConfig{Driver: config.DriverSQLite, URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		defer sqlDB.Close()
		if _, err := sqlDB.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, n INTEGER)`); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		errs := make(chan error, 200)
		for w := range 8 {
			wg.Go(func() {
				for i := range 25 {
					if _, err := sqlDB.Exec(`INSERT INTO t (n) VALUES (?)`, w*100+i); err != nil {
						errs <- err
					}
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("%s: %v", uri, err)
			break
		}
	}
}

func TestSQLiteDSNKeepsExplicitBusyTimeout(t *testing.T) {
	cases := map[string]string{
		"app.db":                              "app.db?_pragma=busy_timeout(5000)",
		"sqlite://file:app.db?mode=ro":        "file:app.db?mode=ro&_pragma=busy_timeout(5000)",
		"file:app.db?_pragma=busy_timeout(1)": "file:app.db?_pragma=busy_timeout(1)",
	}
	for in, want := range cases {
		if got := sqliteDSN(in); got != want {
			t.Errorf("sqliteDSN(%q) = %q, want %q", in, got, want)
		}
	}
}
