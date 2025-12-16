// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import "errors"

var (
	// ErrInvalidArgument reports an invalid configuration or nil reader/writer.
	ErrInvalidArgument = errors.New("framer: invalid argument")

	// ErrTooLong reports that a frame length exceeds limits or the supported wire format.
	ErrTooLong = errors.New("framer: message too long")
)
