//nolint:errwrap
package a

// Good: file-level nolint suppresses all errors.
func returnUnwrappedInNolintFile(err error) error {
	return err
}
