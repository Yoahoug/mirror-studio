package media

import (
	"errors"
	"fmt"
)

// Errors that the HTTP layer maps onto the reference's exception handling.
var (
	// ErrUploadSessionNotFound is the reference's FileNotFoundError: the
	// handler answers with the framework's HTML 404 page.
	ErrUploadSessionNotFound = errors.New("上传会话不存在")
	// ErrImageNotFound covers the download-token lookup, which raises Http404.
	ErrImageNotFound = errors.New("图片不存在")
	// ErrImageUnavailable is raised when an object is read from a row that is
	// no longer available.
	ErrImageUnavailable = errors.New("图片已不可用")
)

// invalidValueError is the Go counterpart of the reference's ValueError: the
// HTTP layer answers with code 400100 and the message verbatim.
type invalidValueError struct{ message string }

func (e invalidValueError) Error() string { return e.message }

func invalidValue(format string, args ...any) error {
	if len(args) == 0 {
		return invalidValueError{message: format}
	}
	return invalidValueError{message: fmt.Sprintf(format, args...)}
}

// invalidMessage reports the verbatim message of a validation failure.
func invalidMessage(err error) (string, bool) {
	var target invalidValueError
	if errors.As(err, &target) {
		return target.message, true
	}
	return "", false
}
