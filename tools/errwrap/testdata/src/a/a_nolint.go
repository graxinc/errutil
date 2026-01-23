//errwrap:ignore
package a

// Good: file-level ignore suppresses all errors.
func returnUnwrappedInNolintFile(err error) error {
	return err
}
