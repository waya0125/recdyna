# recdyna

ダイナコネクティブ（Dynaconnective）の USB 地デジチューナー DY-UD200 を Linux で使う、録画コマンドです。  
Go だけで書いた 1 つの静的バイナリで、cgo・libusb・libpcsclite を使わず、Linux の `usbfs` と `pcscd` に直接つなぎます。  
無改造ファームウェア（初期版・再販品 `1.0.5-2`）の制御コマンドの暗号化と TS の XOR 復号に対応しており、本体のスロットに挿した B-CAS カードでスクランブルを外して録画できます（`--b25`）。

## 対応機種

| 機種 | USB ID | 放送波 | B-CAS | 詳細・動作確認 |
|---|---|---|---|---|
| Dynaconnective DY-UD200 | `1c11:1004` | 地デジ・CATV・ワンセグ（1 チューナー） | 本体のスロット（外付けのカードリーダーも可） | [docs/DY-UD200.md](docs/DY-UD200.md) |

ほかの機種にも広げられるように、`--model` オプションと、機種ごとのパッケージ（`internal/dyud200` など）に分けています。

## 関連プロジェクト

- Windows（TVTest・EDCB）で使う BonDriver: [BonDriver_Dyna](https://github.com/waya0125/BonDriver_Dyna)
- ファームウェア管理ツール（抽出・パッチ・書き込み）: [dynafw](https://github.com/waya0125/dynafw)
- 手元のチューナーの動作確認の一覧: [チューナー動作確認一覧](https://waya0125.github.io/tuners/)

## 必要なもの

- B-CAS カード（本体のスロットに挿す。外付けのカードリーダーを使う場合は `pcscd`）
- ファームウェアやメーカーのドライバーは不要です。

メーカーは 2010 年 10 月に自己破産を申請し、公式のサイトはなくなっています。  
機器の情報は次の有志の資料にあります。

- 有志の BonDriver_dyud の配布ページ（[Wayback Machine](http://web.archive.org/web/20130527152129/http://dyud200.zxq.net/)）
- 有志のまとめ Wiki「[USB地デジチューナ DY-UD200 まとめwiki](https://w.atwiki.jp/dy-ud200/)」

## ビルド

Go 1.24 以上が必要です。

```bash
CGO_ENABLED=0 go build -o bin/ ./cmd/...
```

手元に Go を入れない場合は、Docker の Go でビルド・テストできます。

```bash
scripts/go.sh test ./...
scripts/go.sh build -o bin/ ./cmd/...
```

## 準備

一般ユーザー（`plugdev` グループ）から DY-UD200 を開けるように、udev ルールを置きます。

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

## 注意事項

- 地デジ専用です。BS・CS は受けられません。
- `--card-log` の記録には鍵の情報が入ります。人に渡さないでください。
- 選局の直後、約 1 秒は TS が乱れます。  
  既定では安定するまで出力を待つので、録画の頭が 1 秒あまり遅れます（`--no-settle` で無効にできます）。
- 純正ファームウェア（`1.0.5-2` など）では、1 時間に数個〜数十個の割合で TS が欠けることがあります。  
  機器の中で起きる既知の不具合で、recdyna では直せません。  
  Drop 低減パッチを当てたカスタムファームウェア（`1.0.5-3` など）の利用を強く推奨します。  
  原因の解析や [dynafw](https://github.com/waya0125/dynafw) を使った入れ替え・戻し方の手順は [docs/DY-UD200.md](docs/DY-UD200.md#63-カスタムファームウェア105-3の分析と入れ替え手順) を参照してください。  
  `1.0.5-3` を検出した場合は自動で TS の XOR 復号を無効化します（`1.0.2-2改` などバージョン表記が変わらないカスタム FW では `--no-xor` を付けて使います）。

## 出典・ライセンス

詳細は [LICENSE](LICENSE) を参照してください。

- **プロトコル参考**: 有志ドライバー **BonDriver_dyud v8.5.3**（2010 年公開、旧配布元 `http://dyud200.zxq.net/`、Wayback Machine 2013-05-27 アーカイブ）
- **ARIB STD-B25 実装原典**: **arib25**（ARIB STD-B25 仕様確認テストプログラム、茂木 和洋 作、[LICENSES/arib25.txt](LICENSES/arib25.txt)）
- **安定化フィルター参考**: **coax**（`https://github.com/Till0196/coax`）
- **その他すべての実装・文書**: Copyright (c) 2026 waya0125（MIT License、[LICENSES/MIT.txt](LICENSES/MIT.txt)）
