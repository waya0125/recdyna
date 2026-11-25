// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// recdyna はダイナコネクティブ製 USB 地上デジタルチューナーで受信し、MPEG-2 TS を出力する。
//
//	recdyna [オプション] チャンネル 秒 出力先
//	  チャンネル: 13〜62（UHF）、1〜12（VHF）、C13〜C63（CATV。K13 とも書ける）
//	  秒:         - なら SIGINT/SIGTERM で止められるまで
//	  出力先:     - なら標準出力
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/waya0125/recdyna/internal/dyud200"
	"github.com/waya0125/recdyna/internal/tscheck"
)

const usage = `使い方: recdyna [オプション] チャンネル 秒 出力先
  チャンネル  13〜62（UHF）、1〜12（VHF）、C13〜C63（CATV、K13 表記も可）
  秒          録画秒数。- で停止シグナルを受けるまで継続
  出力先      出力ファイルパス。- で標準出力

オプション:
`

type options struct {
	b25      bool
	pcsc     bool
	round    int
	strip    bool
	sync     bool
	stats    bool
	noSettle bool
	noXOR    bool
	oneSeg   bool
	check    bool
	model    string
	sid      string
	device   string
	raw      string
	cardLog  string
	channel  int
	catv     bool
	seconds  int // 0 なら止められるまで
	output   string
	freqKHz  uint32
	exitCode int
}

func parseArgs(args []string, stderr io.Writer) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("recdyna", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	fs.BoolVar(&o.b25, "b25", false, "本体内蔵 B-CAS スロットで ARIB STD-B25 スクランブルを復号する")
	fs.BoolVar(&o.pcsc, "pcsc", false, "--b25 使用時に内蔵スロットの代わりに PC/SC カードリーダーを使用する")
	fs.IntVar(&o.round, "round", 4, "MULTI2 の段数")
	fs.BoolVar(&o.strip, "strip", false, "ヌルパケット（PID 0x1fff）を除去する")
	fs.BoolVar(&o.sync, "sync", false, "--b25 使用時に鍵が揃うまでのエラーを待機する")
	fs.BoolVar(&o.stats, "stats", false, "終了時に TS ヘッダーの統計（同期・TEI・CC エラー・PID）を出力する")
	fs.BoolVar(&o.noSettle, "no-settle", false, "選局直後の不安定区間（同期崩れ・TEI・CC 飛び）を破棄せずに出力する")
	fs.BoolVar(&o.noXOR, "no-xor", false, "TS の XOR 鍵設定と復号を行わない（パッチ済みファームウェア用）")
	fs.BoolVar(&o.oneSeg, "oneseg", false, "ワンセグモードで受信する")
	fs.BoolVar(&o.check, "check", false, "選局してロック状態と信号強度を表示して終了する")
	fs.StringVar(&o.model, "model", "auto", "対象機種（auto, dy-ud200）")
	fs.StringVar(&o.sid, "sid", "", "サービス ID 指定（互換用・無視）")
	fs.StringVar(&o.device, "device", "", "使用する機器ファイル（/dev/bus/usb/BBB/DDD）。空の場合は空き機器を自動選択")
	fs.StringVar(&o.raw, "raw", "", "B25 復号前の TS を別ファイルへ同時出力する（検証用）")
	fs.StringVar(&o.cardLog, "card-log", "", "B-CAS カードとの APDU やり取りを記録する（検証用。鍵情報を含むため取扱注意）")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	switch strings.ToLower(o.model) {
	case "", "auto", "dy-ud200", "dyud200":
		o.model = "dy-ud200"
	default:
		return nil, fmt.Errorf("未対応の機種 %q です（対応: auto, dy-ud200）", o.model)
	}
	if o.check {
		if fs.NArg() < 1 {
			fs.Usage()
			return nil, errors.New("--check にはチャンネルを指定してください")
		}
	} else if fs.NArg() != 3 {
		fs.Usage()
		return nil, errors.New("チャンネル・秒・出力先の 3 つを指定してください")
	}

	ch := strings.ToUpper(fs.Arg(0))
	if strings.HasPrefix(ch, "C") || strings.HasPrefix(ch, "K") {
		o.catv = true
		ch = ch[1:]
	}
	n, err := strconv.Atoi(ch)
	if err != nil {
		return nil, fmt.Errorf("チャンネル %q を解釈できません", fs.Arg(0))
	}
	o.channel = n
	if o.freqKHz, err = dyud200.Frequency(o.channel, o.catv); err != nil {
		return nil, err
	}
	if o.check {
		return o, nil
	}
	if fs.Arg(1) != "-" {
		if o.seconds, err = strconv.Atoi(fs.Arg(1)); err != nil || o.seconds <= 0 {
			return nil, fmt.Errorf("秒数 %q を解釈できません", fs.Arg(1))
		}
	}
	o.output = fs.Arg(2)
	if o.pcsc && !o.b25 {
		return nil, errors.New("--pcsc は --b25 と併用してください")
	}
	return o, nil
}

func main() {
	o, err := parseArgs(os.Args[1:], os.Stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
	if err := run(o, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(o.exitCode)
}

func stripNullPackets(packets []byte) []byte {
	out := make([]byte, 0, len(packets))
	for p := 0; p+dyud200.PacketSize <= len(packets); p += dyud200.PacketSize {
		pid := uint16(packets[p+1]&0x1f)<<8 | uint16(packets[p+2])
		if pid == 0x1fff {
			continue
		}
		out = append(out, packets[p:p+dyud200.PacketSize]...)
	}
	return out
}

func run(o *options, log io.Writer) error {
	dev, err := dyud200.OpenDevice(o.device)
	if err != nil {
		return err
	}
	defer dev.Close()
	fmt.Fprintf(log, "device: %s\n", dev.USB.Path())
	dev.Log = log

	if err := dev.Init(); err != nil {
		return err
	}

	enableXOR := !o.noXOR
	if enableXOR && dev.FW.IsXorBypassed() {
		enableXOR = false
		fmt.Fprintf(log, "FW %s detected: disabling TS XOR decryption automatically\n", dev.FW)
	}

	if o.check {
		if err := dev.Tune(o.freqKHz, o.oneSeg, enableXOR); err != nil {
			return err
		}
		lockStatus, locked := dev.WaitLock(5 * time.Second)
		fmt.Fprintf(log, "lock: status=%d locked=%v\n", lockStatus, locked)
		if sig, rawSig, err := dev.GetSignalLevel(); err == nil {
			fmt.Fprintf(log, "signal: %.4f (raw %#08x)\n", sig, rawSig)
		}
		if !locked {
			o.exitCode = 1
		}
		return nil
	}

	var decoder b25Decoder
	if o.b25 {
		decoder, err = newDecoder(o, dev, log)
		if err != nil {
			fmt.Fprintln(log, err)
			fmt.Fprintln(log, "disable b25 decoding.")
			decoder = nil
			o.exitCode = 1
		} else {
			defer decoder.Close()
		}
	}

	if err := dev.Tune(o.freqKHz, o.oneSeg, enableXOR); err != nil {
		return err
	}

	lockStatus, locked := dev.WaitLock(5 * time.Second)
	fmt.Fprintf(log, "lock: status=%d locked=%v\n", lockStatus, locked)
	if sig, rawSig, err := dev.GetSignalLevel(); err == nil {
		fmt.Fprintf(log, "signal: %.4f (raw %#08x)\n", sig, rawSig)
	}

	var out io.Writer = os.Stdout
	if o.output != "-" {
		file, err := os.Create(o.output)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}
	var raw *os.File
	if o.raw != "" {
		if raw, err = os.Create(o.raw); err != nil {
			return err
		}
		defer raw.Close()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGPIPE)
	defer signal.Stop(signals)

	fmt.Fprintln(log, "Output ts file.")
	start := time.Now()
	var deadline <-chan time.Time
	if o.seconds > 0 {
		timer := time.NewTimer(time.Duration(o.seconds) * time.Second)
		defer timer.Stop()
		deadline = timer.C
	}

	stream, err := dev.StartStream()
	if err != nil {
		return err
	}
	xorCipher, _ := dev.XORCipherCopy()
	aligner := dyud200.NewAligner(xorCipher)
	var settler *dyud200.Settler
	if !o.noSettle {
		settler = dyud200.NewSettler()
	} else {
		aligner.Log = log
	}
	var stats *tscheck.Stats
	if o.stats {
		stats = tscheck.New()
	}
	b25Errors := 0
	synced := false
	write := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if stats != nil {
			stats.Add(data)
		}
		_, err := out.Write(data)
		return err
	}

	var writeErr error
loop:
	for {
		select {
		case <-deadline:
			break loop
		case s := <-signals:
			fmt.Fprintf(log, "interrupted (%v).\n", s)
			break loop
		case chunk, ok := <-stream.Chunks:
			if !ok {
				fmt.Fprintln(log, "the USB stream ended.")
				break loop
			}
			prevResyncs := aligner.Resyncs
			packets := aligner.Feed(chunk)
			if settler != nil && aligner.Resyncs != prevResyncs {
				settler.ResetRun()
			}
			if len(packets) == 0 {
				continue
			}
			if settler != nil {
				wasSettled := settler.Settled()
				packets = settler.Feed(packets)
				if !wasSettled && settler.Settled() {
					aligner.Log = log
					if settler.GaveUp {
						fmt.Fprintf(log, "settle: gave up after dropping %d packets\n", settler.Dropped)
					} else if settler.Dropped > 0 {
						fmt.Fprintf(log, "settle: dropped %d initial unstable packets\n", settler.Dropped)
					}
				}
				if len(packets) == 0 {
					continue
				}
			}
			if raw != nil {
				if _, err := raw.Write(packets); err != nil {
					writeErr = err
					break loop
				}
			}
			if decoder != nil {
				decoded, err := decoder.Put(packets)
				if err != nil {
					if !synced && o.sync {
						fmt.Fprintln(log, "Wait for B25 sync")
						continue
					}
					b25Errors++
					if b25Errors == b25ErrorMax {
						fmt.Fprintf(log, "B25 Error: %v\n", err)
						fmt.Fprintln(log, "Continue recording without B25.")
						decoder.Close()
						decoder = nil
						o.exitCode = 1
					}
					continue
				}
				if len(decoded) > 0 {
					synced = true
				}
				packets = decoded
			} else if o.strip {
				packets = stripNullPackets(packets)
			}
			if err := write(packets); err != nil {
				writeErr = err
				break loop
			}
		}
	}
	stream.Stop()
	elapsed := time.Since(start)
	if stream.Dropped > 0 || stream.Errors > 0 {
		fmt.Fprintf(log, "USB: dropped %d chunks, %d failed transfers (last: %v)\n", stream.Dropped, stream.Errors, stream.LastError)
	}
	if decoder != nil && writeErr == nil {
		rest, err := decoder.Flush()
		if err != nil {
			fmt.Fprintf(log, "B25 Error: %v\n", err)
			o.exitCode = 1
		}
		writeErr = write(rest)
	}
	if writeErr != nil {
		fmt.Fprintf(log, "write failed: %v\n", writeErr)
		o.exitCode = 1
	}
	fmt.Fprintln(log, "done.")
	fmt.Fprintf(log, "Rec time: %.6f sec.\n", elapsed.Seconds())
	if sig, rawSig, err := dev.GetSignalLevel(); err == nil {
		fmt.Fprintf(log, "signal: %.4f (raw %#08x)\n", sig, rawSig)
	}
	if stats != nil {
		stats.Print(log, 10)
	}
	return nil
}

const b25ErrorMax = 100

type b25Decoder interface {
	Put(packets []byte) ([]byte, error)
	Flush() ([]byte, error)
	Close()
}
