package main

import "fmt"

// cliError carries the process exit code alongside a user-facing message so
// exitCodeFor never has to guess from error text.
type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

// usageErrorf marks usage errors or unsupported input (exit 2).
func usageErrorf(format string, args ...any) error {
	return &cliError{code: exitUsage, err: fmt.Errorf(format, args...)}
}

// notFoundErrorf marks missing tools, versions or defaults (exit 3).
func notFoundErrorf(format string, args ...any) error {
	return &cliError{code: exitNotFound, err: fmt.Errorf(format, args...)}
}

// stateErrorf marks local state conflicts such as refused overwrites (exit 6).
func stateErrorf(format string, args ...any) error {
	return &cliError{code: exitState, err: fmt.Errorf(format, args...)}
}

// integrityErrorf marks integrity or archive-safety refusals (exit 5).
func integrityErrorf(format string, args ...any) error {
	return &cliError{code: exitIntegrity, err: fmt.Errorf(format, args...)}
}
