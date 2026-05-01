package middleware

// NewWithKeyfunc exposes the unexported test helper newWithKeyfunc to
// black-box (`package middleware_test`) test files. Same shape and
// contract as newWithKeyfunc — see auth.go for the comment.
//
// Internal (`package middleware`) test files in this package keep using
// newWithKeyfunc directly.
var NewWithKeyfunc = newWithKeyfunc
