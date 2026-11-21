// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package dyud200

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// defaultAESKey は DY-UD200 の制御コマンド初期化・応答復号に使われる AES-256 鍵。
// BonDriver_dyud v8.5.3 (BonDriver_dyud.h) より移植。
var defaultAESKey = [32]byte{
	0x75, 0x93, 0xee, 0x38, 0x0d, 0x99, 0x44, 0x95,
	0x9f, 0xc7, 0x67, 0x2c, 0x59, 0xeb, 0x1f, 0x69,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

// defaultXORKey は SetTSXorKey コマンドのペイロード暗号化に使われる既定 XOR 鍵。
var defaultXORKey = [32]byte{
	0xFA, 0x80, 0x09, 0x1A, 0x97, 0xE0, 0x4F, 0xC8,
	0x0A, 0x59, 0xBA, 0xC2, 0xB6, 0xCD, 0x65, 0x2C,
	0xC1, 0x7A, 0xCE, 0xC5, 0x18, 0x42, 0x91, 0x45,
	0xA5, 0x59, 0x7E, 0xA4, 0xD8, 0xB1, 0xCC, 0x83,
}

// sessionXORKey は BonDriver_dyud が選局時に設定する 32 バイトのセッション XOR 鍵。
var sessionXORKey = [32]byte{
	0x93, 0xAD, 0x82, 0xA2, 0x82, 0xBD, 0x82, 0xE7,
	0x82, 0xDC, 0x82, 0xAF, 0x82, 0xC5, 0x82, 0xB2,
	0x82, 0xB4, 0x82, 0xE9, 0x81, 0x49, 0x95, 0x89,
	0x82, 0xAF, 0x82, 0xC5, 0x82, 0xB2, 0x82, 0xB4,
}

// cmdCipher は DY-UD200 の USB 制御コマンド（132B 送信 / 128B 受信）の AES-ECB + CRC32 変換を行う。
type cmdCipher struct {
	defaultBlock cipher.Block // AES-256
	cmdBlock     cipher.Block // AES-128（初期化コマンドの [16:32] で更新される）
}

func newCmdCipher() *cmdCipher {
	def, err := aes.NewCipher(defaultAESKey[:])
	if err != nil {
		panic(err)
	}
	cmd, err := aes.NewCipher(defaultAESKey[:16])
	if err != nil {
		panic(err)
	}
	return &cmdCipher{
		defaultBlock: def,
		cmdBlock:     cmd,
	}
}

// EncryptCommand はコマンド列（最大 128 バイト）を 132 バイトの暗号化フレームに変換する。
// 先頭バイトの bit 4 (0x10) が 0 の場合は AES-256 既定鍵で暗号化し、plain[16:32] を以後の AES-128 コマンド鍵に設定する。
func (c *cmdCipher) EncryptCommand(cmd []byte) ([132]byte, error) {
	var plain, enc [132]byte
	if len(cmd) == 0 || len(cmd) > 128 {
		return enc, fmt.Errorf("不正なコマンド長: %d", len(cmd))
	}
	copy(plain[:], cmd)
	crc := crc32.ChecksumIEEE(plain[:128])
	binary.BigEndian.PutUint32(plain[128:132], crc)

	var blk cipher.Block
	if plain[0]&0x10 != 0 {
		blk = c.cmdBlock
	} else {
		blk = c.defaultBlock
		next, err := aes.NewCipher(plain[16:32])
		if err != nil {
			return enc, err
		}
		c.cmdBlock = next
	}

	// 先頭 2 バイトと末尾 2 バイトは平文のまま、間の 128 バイト ([2:130]) を AES-ECB で暗号化する
	enc[0], enc[1] = plain[0], plain[1]
	enc[130], enc[131] = plain[130], plain[131]
	for off := 2; off < 130; off += aes.BlockSize {
		blk.Encrypt(enc[off:off+aes.BlockSize], plain[off:off+aes.BlockSize])
	}
	return enc, nil
}

// DecryptResponse は 128 バイトの応答フレームを既定 AES-256 鍵で復号し、CRC32 を検証する。
func (c *cmdCipher) DecryptResponse(enc []byte) ([128]byte, error) {
	var plain [128]byte
	if len(enc) != 128 {
		return plain, fmt.Errorf("応答長が 128 バイトではない: %d", len(enc))
	}
	for off := 0; off < 128; off += aes.BlockSize {
		c.defaultBlock.Decrypt(plain[off:off+aes.BlockSize], enc[off:off+aes.BlockSize])
	}
	want := binary.BigEndian.Uint32(plain[124:128])
	got := crc32.ChecksumIEEE(plain[:124])
	if got != want {
		return plain, fmt.Errorf("応答 CRC32 不一致: got=%#08x want=%#08x", got, want)
	}
	return plain, nil
}

// XORCipher は DY-UD200 の TS パケット XOR 変換（dyud_xor.cpp 互換）を行う。
type XORCipher struct {
	lut [514]byte
}

// SetKey は鍵から LUT を初期化する（CreateLUT 互換）。
func (x *XORCipher) SetKey(key []byte) {
	x.lut[512] = 1
	x.lut[513] = 0
	for i := range 256 {
		x.lut[i] = byte(i)
	}
	var keyOffset, shuffleIndex byte
	for i := range 256 {
		ex := x.lut[i]
		shuffleIndex += key[keyOffset] + x.lut[i]
		x.lut[i] = x.lut[shuffleIndex]
		x.lut[shuffleIndex] = ex
		keyOffset++
		if int(keyOffset) == len(key) {
			keyOffset = 0
		}
	}
	copy(x.lut[256:512], x.lut[0:256])
}

// Crypt は src を LUT で XOR 変換して dst に書き込む（XORcrypt 互換）。dst と src は同一スライス可。
func (x *XORCipher) Crypt(dst, src []byte) {
	p1, p2 := x.lut[512], x.lut[513]
	for i, b := range src {
		t := x.lut[p1]
		p1++
		p2 += t
		t += x.lut[p2]
		dst[i] = b ^ x.lut[t]
	}
}

// DecryptPacket は 188 バイトの TS パケットに DY-UD200 の XOR 暗号がかかっていれば復号する。
// 条件は BonDriver_dyud.cpp の GetTsStream と同一:
//   - PID >= 0x10 && PID != 0x1fff
//   - transport_scrambling_control >= 2
//   - transport_priority == 1（復号時に 0 へ落とす）
func (x *XORCipher) DecryptPacket(pkt []byte) {
	if len(pkt) < 188 {
		return
	}
	pid := (uint16(pkt[1]&0x1f) << 8) | uint16(pkt[2])
	if pid < 0x10 || pid == 0x1fff {
		return
	}
	if (pkt[3] >> 6) < 2 {
		return
	}
	if pkt[1]&0x20 == 0 {
		return
	}
	pkt[1] ^= 0x20
	x.Crypt(pkt[4:132], pkt[4:132])
}
