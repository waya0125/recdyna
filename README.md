# recdyna

ダイナコネクティブ（Dynaconnective）製 USB 地上デジタルチューナー向けの Linux 録画コマンドです。  
cgo・libusb・libpcsclite を使わず、Linux の `usbfs` と `pcscd` と直接通信する単一の静的バイナリとして動作します。

## 対応機種

| 機種 | USB ID | 放送波 | 状態 | 詳細 |
|---|---|---|---|---|
| Dynaconnective **DY-UD200** | `1c11:1004` | 地上デジタル（ISDB-T） | 実機動作確認済み（FW `1.0.5-2`） | [docs/DY-UD200.md](docs/DY-UD200.md) |

※他機種への拡張を見据え、`--model` オプションと機種別パッケージ構成（`internal/dyud200` 等）を採用しています。

## 主な機能

- **純 Go 実装（`CGO_ENABLED=0`）**: 外部共有ライブラリ不要、Linux 環境や Docker コンテナ（Mirakurun 等）へそのまま配置可能
- **地上デジタル選局**: UHF（13〜62ch）・VHF（1〜12ch）・CATV パススルー（C13〜C63ch）、ワンセグ（`--oneseg`）対応
- **FW `1.0.5-2` 自動検出・再初期化**: 初期ファームウェアおよび再販品 FW（`1.0.5-2`）の両方に対応
- **TS XOR 復号対応**: 無改造 FW のセッション XOR を自動復号（パッチ済み FW 向けに `--no-xor` オプションあり）
- **内蔵 B-CAS スロットによる ARIB STD-B25 復号（`--b25`）**: 本体スロットを USB 経由で直接制御（外付けリーダーは `--pcsc` で利用可能）
- **選局直後の安定化フィルター（`Settler`）**: 選局直後、復調器・FIFO が落ち着くまでの乱れ（同期崩れ・TEI・CC 飛び）を自動で除去し、ゼロドロップで出力を開始（`--no-settle` で無効化可）

## 動作確認（2026-11-22 時点）

| 検証項目 | 結果 |
|---|---|
| 単体テスト（AES テストベクタ・XOR・Settler・B25・MULTI2・B-CAS・PC/SC） | 全パッケージ PASS（`go test ./...`） |
| 実機初期化・FW 検出（Ubuntu 26.04 / amd64） | DY-UD200 FW `1.0.5-2` を検出・再初期化、シリアル番号取得成功 |
| 地デジ 5 チャンネル（ch 21・22・24・25・26、各 10 秒、`--b25 --strip`） | 全 5 チャンネルでドロップ 0・TEI 0・同期崩れ 0・スクランブル残り 0・PCR 飛び 0 |
| 60 秒連続録画（ch 21、`--b25 --strip`、内蔵 B-CAS） | 688,416 パケット（17.21 Mbps）、ドロップ 0・TEI 0・同期崩れ 0・スクランブル残り 0・PCR 飛び 0 |
| Mirakurun 経由 30 分同時撮り比べ（ch 21、対 Sony CECH-ZD1 J） | 20,418,150 パケット（1,795.0 秒・17.11 Mbps）、ドロップ 4 回（率 2.0e-7）・TEI 0・同期崩れ 0・スクランブル残り 0・PCR 飛び 0 |

詳細な仕様・計測結果は [docs/DY-UD200.md](docs/DY-UD200.md) を参照してください。

## ビルド

Go 1.24 以上が必要です。

```bash
CGO_ENABLED=0 go build -o bin/ ./cmd/...
```

ホストに Go を入れずに Docker でビルド・テストすることもできます。

```bash
scripts/go.sh test ./...
scripts/go.sh build -o bin/ ./cmd/...
```

## 準備（udev ルール）

一般ユーザー（`plugdev` グループ）から DY-UD200 を開けるように udev ルールを配置します。

```bash
sudo cp scripts/60-dy-ud200.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules
sudo udevadm trigger --subsystem-match=usb --attr-match=idVendor=1c11 --attr-match=idProduct=1004
```

## 使い方

```bash
bin/recdyna [オプション] チャンネル 秒 出力先
```

| 引数 | 説明 |
|---|---|
| `チャンネル` | `13`〜`62`（UHF）、`1`〜`12`（VHF）、`C13`〜`C63`（CATV、`K13` 表記も可） |
| `秒` | 録画秒数。`-` で SIGINT / SIGTERM を受けるまで継続 |
| `出力先` | 出力ファイルパス。`-` で標準出力 |

| オプション | 説明 |
|---|---|
| `--b25` | 本体内蔵 B-CAS スロットで ARIB STD-B25 スクランブルを復号 |
| `--pcsc` | `--b25` 時に内蔵スロットの代わりに PC/SC カードリーダーを使用（`PCSCLITE_CSOCK_NAME` 対応） |
| `--strip` | ヌルパケット（PID `0x1fff`）を除去 |
| `--sync` | `--b25` で、最初に復号できるまでの失敗を数えずに待機 |
| `--round N` | MULTI2 の段数（既定 `4`） |
| `--stats` | 終了時に TS ヘッダー統計（同期・TEI・CC エラー・PAT TSID・上位 PID）を表示 |
| `--check` | 選局し、ロック状態と信号強度を表示して終了 |
| `--model NAME` | 対象機種（既定 `auto`。対応: `auto`, `dy-ud200`） |
| `--no-settle` | 選局直後の安定化フィルター（`Settler`）を無効化 |
| `--no-xor` | TS の XOR 鍵設定と復号を無効化（パッチ済みファームウェア用） |
| `--oneseg` | ワンセグモードで受信 |
| `--device PATH` | 使用する機器ファイル（`/dev/bus/usb/BBB/DDD`）。省略時は空き機器を自動選択 |
| `--sid` | 互換用（無視） |
| `--raw FILE` | B25 復号前の TS を別ファイルへ同時出力（検証用） |
| `--card-log FILE` | B-CAS カードとの APDU やり取りを記録（検証用。鍵情報を含むため取扱注意） |

### 実行例

```bash
# ch21 を 30 秒録画（内蔵 B-CAS で復号し、ヌルパケットを除去）
bin/recdyna --b25 --strip --stats 21 30 out.ts

# 選局・ロック・信号強度の確認のみ
bin/recdyna --check 21
```

### Mirakurun 連携例（`tuners.yml`）

`recdyna` 自身が内蔵 B-CAS スロットで B25 復号を行うため、`decoder` の指定は不要です。

```yaml
- name: DY-UD200
  types:
    - GR
  command: /usr/local/bin/recdyna --b25 --strip <channel> - -
  isDisabled: false
```

## 出典・ライセンス

詳細は [LICENSE](LICENSE) を参照してください。

- **プロトコル参考**: 有志ドライバー **BonDriver_dyud v8.5.3**（2010 年公開、旧配布元 `http://dyud200.zxq.net/`、Wayback Machine 2013-05-27 アーカイブ）
- **ARIB STD-B25 実装原典**: **arib25**（ARIB STD-B25 仕様確認テストプログラム、茂木 和洋 作、[LICENSES/arib25.txt](LICENSES/arib25.txt)）
- **安定化フィルター参考**: **coax**（`https://github.com/Till0196/coax`）
- **その他すべての実装・文書**: Copyright (c) 2026 waya0125（MIT License、[LICENSES/MIT.txt](LICENSES/MIT.txt)）
