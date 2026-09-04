package controlplaneapi

// NotFound reports a resource the caller may not learn anything about, whether
// it never existed or the caller simply cannot see it. The message is
// deliberately uniform across every API so it leaks nothing either way.
func NotFound() *Error {
	return &Error{
		Code:    CodeNotFound,
		Message: "resource not found",
	}
}

// Invalid rejects one named request field. Field is the client-facing path of
// the offending value so a caller can point at it without parsing the message.
func Invalid(field, message string) *Error {
	return &Error{
		Code:    CodeInvalidArgument,
		Field:   field,
		Message: message,
	}
}
