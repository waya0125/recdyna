// このファイルは arib25（ARIB STD-B25 仕様確認テストプログラム、茂木 和洋 作）の multi2.c を
// Go に移したもの。arib25 の利用条件（LICENSES/arib25.txt）に同意して作った二次的著作物。
// 移植: waya0125（2026）。移植で足した部分は MIT。

// Package multi2 は MULTI2 暗号（ARIB STD-B25 の放送のスクランブル）の CBC・OFB の復号。
package multi2

import "encoding/binary"

type data struct{ l, r uint32 }

type param [8]uint32

// Cipher は 1 つの ECM の鍵（奇数・偶数の組）での復号器。
type Cipher struct {
	cbcInit data
	sys     param
	work    [2]param // 0: 奇数、1: 偶数
	round   int
	keyed   bool
}

// New はシステム鍵（32 バイト）と CBC の初期値（8 バイト）で Cipher を作る。round は段数（普通は 4）。
func New(systemKey []byte, cbcInit []byte, round int) *Cipher {
	c := &Cipher{round: round}
	for i := range c.sys {
		c.sys[i] = binary.BigEndian.Uint32(systemKey[i*4:])
	}
	c.cbcInit = data{binary.BigEndian.Uint32(cbcInit), binary.BigEndian.Uint32(cbcInit[4:])}
	return c
}

// SetScrambleKey は ECM から得たスクランブル鍵（16 バイト。前半が奇数、後半が偶数）を入れる。
func (c *Cipher) SetScrambleKey(key []byte) {
	odd := data{binary.BigEndian.Uint32(key), binary.BigEndian.Uint32(key[4:])}
	even := data{binary.BigEndian.Uint32(key[8:]), binary.BigEndian.Uint32(key[12:])}
	c.work[0] = schedule(&c.sys, odd)
	c.work[1] = schedule(&c.sys, even)
	c.keyed = true
}

// ClearScrambleKey は鍵を消す。
func (c *Cipher) ClearScrambleKey() {
	c.work = [2]param{}
	c.keyed = false
}

// Keyed は鍵が入っているか。
func (c *Cipher) Keyed() bool { return c.keyed }

// Decrypt は buf をその場で復号する。scrambling は TS ヘッダーの transport_scrambling_control
// （2 なら偶数の鍵、それ以外は奇数の鍵）。
func (c *Cipher) Decrypt(scrambling int, buf []byte) {
	w := &c.work[0]
	if scrambling == 2 {
		w = &c.work[1]
	}
	cbc := c.cbcInit
	p := buf
	for len(p) >= 8 {
		src := data{binary.BigEndian.Uint32(p), binary.BigEndian.Uint32(p[4:])}
		dst := decryptBlock(src, w, c.round)
		dst.l ^= cbc.l
		dst.r ^= cbc.r
		cbc = src
		binary.BigEndian.PutUint32(p, dst.l)
		binary.BigEndian.PutUint32(p[4:], dst.r)
		p = p[8:]
	}
	if len(p) > 0 {
		// 端は OFB
		dst := encryptBlock(cbc, w, c.round)
		var tmp [8]byte
		binary.BigEndian.PutUint32(tmp[:], dst.l)
		binary.BigEndian.PutUint32(tmp[4:], dst.r)
		for i := range p {
			p[i] ^= tmp[i]
		}
	}
}

// Encrypt は buf をその場で暗号化する（試験用）。
func (c *Cipher) Encrypt(scrambling int, buf []byte) {
	w := &c.work[0]
	if scrambling == 2 {
		w = &c.work[1]
	}
	dst := c.cbcInit
	p := buf
	for len(p) >= 8 {
		src := data{binary.BigEndian.Uint32(p) ^ dst.l, binary.BigEndian.Uint32(p[4:]) ^ dst.r}
		dst = encryptBlock(src, w, c.round)
		binary.BigEndian.PutUint32(p, dst.l)
		binary.BigEndian.PutUint32(p[4:], dst.r)
		p = p[8:]
	}
	if len(p) > 0 {
		dst = encryptBlock(dst, w, c.round)
		var tmp [8]byte
		binary.BigEndian.PutUint32(tmp[:], dst.l)
		binary.BigEndian.PutUint32(tmp[4:], dst.r)
		for i := range p {
			p[i] ^= tmp[i]
		}
	}
}

func rol(v uint32, n uint) uint32 { return v<<n | v>>(32-n) }

func pi1(s data) data { return data{s.l, s.r ^ s.l} }

func pi2(s data, a uint32) data {
	t0 := s.r + a
	t1 := rol(t0, 1) + t0 - 1
	t2 := rol(t1, 4) ^ t1
	return data{s.l ^ t2, s.r}
}

func pi3(s data, a, b uint32) data {
	t0 := s.l + a
	t1 := rol(t0, 2) + t0 + 1
	t2 := rol(t1, 8) ^ t1
	t3 := t2 + b
	t4 := rol(t3, 1) - t3
	t5 := rol(t4, 16) ^ (t4 | s.l)
	return data{s.l, s.r ^ t5}
}

func pi4(s data, a uint32) data {
	t0 := s.r + a
	t1 := rol(t0, 2) + t0 + 1
	return data{s.l ^ t1, s.r}
}

func schedule(sys *param, key data) param {
	var w param
	b1 := pi1(key)
	b2 := pi2(b1, sys[0])
	w[0] = b2.l
	b3 := pi3(b2, sys[1], sys[2])
	w[1] = b3.r
	b4 := pi4(b3, sys[3])
	w[2] = b4.l
	b5 := pi1(b4)
	w[3] = b5.r
	b6 := pi2(b5, sys[4])
	w[4] = b6.l
	b7 := pi3(b6, sys[5], sys[6])
	w[5] = b7.r
	b8 := pi4(b7, sys[7])
	w[6] = b8.l
	b9 := pi1(b8)
	w[7] = b9.r
	return w
}

func encryptBlock(d data, w *param, round int) data {
	for range round {
		d = pi1(d)
		d = pi2(d, w[0])
		d = pi3(d, w[1], w[2])
		d = pi4(d, w[3])
		d = pi1(d)
		d = pi2(d, w[4])
		d = pi3(d, w[5], w[6])
		d = pi4(d, w[7])
	}
	return d
}

func decryptBlock(d data, w *param, round int) data {
	for range round {
		d = pi4(d, w[7])
		d = pi3(d, w[5], w[6])
		d = pi2(d, w[4])
		d = pi1(d)
		d = pi4(d, w[3])
		d = pi3(d, w[1], w[2])
		d = pi2(d, w[0])
		d = pi1(d)
	}
	return d
}
