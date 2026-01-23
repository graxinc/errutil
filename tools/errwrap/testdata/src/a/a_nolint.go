//errwrap:unwrapped
package a

func returnUnwrappedInNolintFile(err error) error {
	return err
}
