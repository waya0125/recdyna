// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/waya0125/recdyna/internal/b25"
	"github.com/waya0125/recdyna/internal/bcas"
	"github.com/waya0125/recdyna/internal/dyud200"
)

type decoder struct {
	*b25.Decoder
	closers []io.Closer
}

func (d *decoder) Close() {
	for _, c := range d.closers {
		c.Close()
	}
	d.closers = nil
}

func newDecoder(o *options, dev *dyud200.Device, log io.Writer) (b25Decoder, error) {
	var transport bcas.Transport
	d := &decoder{}
	if o.pcsc {
		reader, err := openPCSC(log)
		if err != nil {
			return nil, err
		}
		d.closers = append(d.closers, reader)
		transport = reader
	} else {
		fmt.Fprintln(log, "B-CAS: built-in slot of the tuner")
		reader := dyud200.NewCardReader(dev)
		reader.Log = log
		transport = reader
	}
	if o.cardLog != "" {
		file, err := os.OpenFile(o.cardLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			d.Close()
			return nil, err
		}
		d.closers = append(d.closers, file)
		transport = &loggingTransport{t: transport, w: file}
	}
	card, err := bcas.Open(transport)
	if err != nil {
		d.Close()
		return nil, err
	}
	d.Decoder = b25.New(card, o.round, o.strip)
	fmt.Fprintln(log, "B25Decoder initialized.")
	return d, nil
}

type loggingTransport struct {
	t bcas.Transport
	w io.Writer
}

func (l *loggingTransport) Reset() error {
	fmt.Fprintln(l.w, "reset")
	return l.t.Reset()
}

func (l *loggingTransport) Transmit(apdu []byte) ([]byte, error) {
	r, err := l.t.Transmit(apdu)
	if err != nil {
		fmt.Fprintf(l.w, "> %s\n! %v\n", hex.EncodeToString(apdu), err)
		return r, err
	}
	fmt.Fprintf(l.w, "> %s\n< %s\n", hex.EncodeToString(apdu), hex.EncodeToString(r))
	return r, nil
}
