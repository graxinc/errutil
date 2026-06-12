package errutil

type Tags map[string]any

func With(err error) error                            { return err }
func Witht(err error, t Tags) error                   { return err }
func Wrap(err error, allowed ...error) error          { return err }
func Wrapt(err error, t Tags, allowed ...error) error { return err }
func New(t Tags) error                                { return nil }
