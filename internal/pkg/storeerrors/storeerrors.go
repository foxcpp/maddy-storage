package storeerrors

type LogicError struct {
	Text  string
	Cause error
}

func (e LogicError) Error() string {
	return e.Text
}

func (e LogicError) Unwrap() error {
	return e.Cause
}

type InternalError struct {
	Reason error
}

func (e InternalError) Error() string {
	return e.Reason.Error()
}

func (e InternalError) Unwrap() error {
	return e.Reason
}

type AlreadyExistsError struct {
	Text  string
	Cause error
}

func (e AlreadyExistsError) Error() string {
	return e.Text
}

func (e AlreadyExistsError) Unwrap() error {
	return e.Cause
}

type NotExistsError struct {
	Text  string
	Cause error
}

func (e NotExistsError) Error() string {
	return e.Text
}

func (e NotExistsError) Unwrap() error {
	return e.Cause
}

type ValidationError struct {
	Text  string // user text, optional
	Field string
	Cause error
}

func (e ValidationError) Error() string {
	if e.Cause != nil {
		return "invalid value for field " + e.Field + ": " + e.Cause.Error()
	}
	return "invalid value for field " + e.Field
}

func (e ValidationError) Unwrap() error {
	return e.Cause
}

func IsTemporary(err error) bool {
	return false
}
