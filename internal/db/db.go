package db

import (
	"database/sql"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

var MaxPageSize = 100

func New(dsn string) (*sql.DB, error) {
	db, err := sql.Open("libsql", dsn)

	if err != nil {
		return nil, err
	}

	// stops STREAM_EXPIRED issues with connections timing out but not released
	db.SetConnMaxIdleTime(9)

	return db, nil
}
