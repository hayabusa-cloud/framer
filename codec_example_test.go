// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"fmt"

	"code.hybscloud.com/framer"
)

func ExampleDecoder() {
	var d framer.Decoder
	for _, input := range [][]byte{{3, 'a'}, {'b', 'c', 0}} {
		for len(input) > 0 {
			n, chunk, err := d.Decode(input)
			if chunk.Begin {
				fmt.Println("begin", chunk.Length)
			}
			if len(chunk.Data) > 0 {
				fmt.Printf("data %s\n", chunk.Data)
			}
			if chunk.End {
				fmt.Println("end")
			}
			input = input[n:]
			if err != nil && err != framer.ErrMore {
				panic(err)
			}
		}
	}
	if err := d.Finish(); err != nil {
		panic(err)
	}
	// Output:
	// begin 3
	// data a
	// data bc
	// end
	// begin 0
	// end
}

func ExampleEncodeHeader() {
	var header [framer.MaxHeaderSize]byte
	n, err := framer.EncodeHeader(header[:], 300, nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%x\n", header[:n])
	// Output: fe012c
}
