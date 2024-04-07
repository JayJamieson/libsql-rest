package db

import "database/sql"

func New(dsn string) (*sql.DB, error) {
	db, err := sql.Open("libsql", dsn)

	if err != nil {
		return nil, err
	}

	// stops STREAM_EXPIRED issues with connections timing out but not released
	db.SetConnMaxIdleTime(9)

	return db, nil
}
