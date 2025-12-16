// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package bo provides native byte order selection.
//
// Implementation is architecture-specific via build tags where commonly known,
// and falls back to a portable runtime detection elsewhere.
package bo
