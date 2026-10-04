package board

// FailedOf is the Failed that Run's executor feeds back when e fails with err: its error names e.
func FailedOf(e Effect, err error) Failed { return Failed{Err: effectErr{effect: e, err: err}} }
