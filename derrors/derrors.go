// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in at
// https://github.com/golang/pkgsite/blob/a1a4503ff625915161dc197111fdd4b3bdcad0ae/LICENSE
package derrors

import "fmt"

// Wrap adds context to the error and allows
// unwrapping the result to recover the original error.
//
// Example:
//
//	defer derrors.Wrap(&err, "copy(%s, %s)", src, dst)
func Wrap(errp *error, format string, args ...any) {
	if *errp != nil {
		*errp = fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), *errp)
	}
}
