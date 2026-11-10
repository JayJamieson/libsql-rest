package store

import (
	"errors"
	"testing"
)

func TestClassifyLibSQLMessage(t *testing.T) {
	err := classify(errors.New("failed to execute SQL: INSERT INTO `accounts` (`email`) VALUES (?) RETURNING *\nSQLite error: UNIQUE constraint failed: accounts.email"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if want := "conflict: UNIQUE constraint failed: accounts.email"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestClassifyLibSQLKeepsParentheses(t *testing.T) {
	err := classify(errors.New("failed to execute SQL: INSERT INTO `paren` (`x`) VALUES (?) RETURNING *\nSQLite error: CHECK constraint failed: x IN (1, 2)"))
	if !errors.Is(err, ErrConstraint) {
		t.Fatalf("err = %v, want ErrConstraint", err)
	}
	if want := "constraint violation: CHECK constraint failed: x IN (1, 2)"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
