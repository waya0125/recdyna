// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package main

import (
	"fmt"
	"io"

	"github.com/waya0125/recdyna/internal/pcsc"
)

type pcscReader interface {
	Reset() error
	Transmit(apdu []byte) ([]byte, error)
	io.Closer
}

func openPCSC(log io.Writer) (pcscReader, error) {
	r, err := pcsc.Open()
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(log, "B-CAS: PC/SC reader %q (%s)\n", r.Name, pcsc.Socket())
	return r, nil
}
