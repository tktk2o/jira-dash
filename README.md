# jira-dash

`gh dash` のような設定駆動のダッシュボードを Jira に対して出す TUI。
見たい範囲を JQL でタブとして定義しておき、そこから参照・作成する。

```
 My Issues │ Example Sprint (15) │ Example Backlog │ Other Backlog
→ PROJ-123  📘  基盤のリファクタリング                          進行中   1h  ╭──────────────
  PROJ-118  🔧  管理画面のログイン機能全般の見直し             To Do    2h  │  ## PROJ-123
```

## 前提

- Go（ビルド時のみ）。jira-dash 自身が Jira API を直接叩くので、外部の CLI は不要
- `~/.config/jira-cli/credentials.json`（または `JIRA_*` 環境変数）に認証情報が
  あること。これは `jira auth login`（本リポジトリの `cmd/jira`）が書き込む
  （なぜ同じファイルを共有するかは [docs/adr/0002](docs/adr/0002-credentials-shared-with-the-cli.md)）
- macOS 前提（`y`/`Y` のクリップボードが `pbcopy`）

## インストール

TUI 本体と、それが使う認証・キーバインドの両方が呼ぶ CLI の、2つのバイナリをビルドする。

```bash
go build -o ~/.local/bin/jira-dash .
go build -o ~/.local/bin/jira ./cmd/jira

# 短縮名。シェルのエイリアスにしないのは、dotfiles の .zshrc が公開リポジトリで
# 追跡されているため。リンクなら PATH の話だけで済む。
ln -s jira-dash ~/.local/bin/jhd

jira auth login

cp config.yml.example config.local.yml
$EDITOR config.local.yml
mkdir -p ~/.config/jira-dash
ln -s "$PWD/config.local.yml" ~/.config/jira-dash/config.yml
```

`scripts/verify-against-old-cli` は、この Go 版 CLI を移行前の TypeScript 版 CLI と
出力差分で比較するスクリプト。旧 CLI がまだ端末に入っている間しか使えない
（経緯は [docs/adr/0001](docs/adr/0001-go-cli-instead-of-the-typescript-one.md)）。

設定は `--config <path>` か `JIRA_DASH_CONFIG` でも指定できる（どちらも先頭の `~` を
自前で展開する）。`config.local.yml` は `.gitignore` 済み — JQL がプロジェクトキーや
スプリント名を含むため、コミットしない。

```
--config <path>      設定ファイル（既定: ~/.config/jira-dash/config.yml）
--section <title>    このタイトルのタブで開く。無い名前なら候補を並べて終了する
--version            バージョンを表示して終了
```

### 設定は起動時に検証する

黙って無視されるより落ちたほうがマシなものは、すべて起動時に落とす。

- **知らないキー**（`limmit:` のような typo）— YAML は既定では黙って捨てるので、
  設定した機能だけが動かない状態になる。設定が悪いと言わせる
- **キーの二重取り** — `create` と `keybindings.issues` が同じキーを持つ、または
  ダッシュボード自身のキー（`j` / `/` / `q` / `r` など）を奪う。押した側は
  handleKey の判定順で黙って負けるので、負けた側が見えるのは起動時だけ
- **`dir` の不在** — 上記のとおり
- **空の値** — title / jql の無い section、type の無い `create`、command の無い
  keybinding

## キー操作

| キー | 動作 |
|------|------|
| `h` / `l` / `←` / `→` / `tab` / `shift+tab` | セクション切り替え |
| `j` / `k` / `gg` / `G` | 移動 |
| `p` | プレビューの開閉 |
| `ctrl+d` / `ctrl+u` | プレビューを半画面スクロール（行カーソルは動かない） |
| `/` | 絞り込み（`esc` で解除）。再取得はしない |
| `r` | このセクションを再取得 |
| `y` / `Y` | 課題キー / URL をコピー |
| `?` | ヘルプ |
| `q` | 終了 |

### 作業ディレクトリ（`dir`）

キーバインドが走るディレクトリは `defaults.dir`、または section ごとの `dir` で
決める（section 側が優先）。ボードとリポジトリは対応するので**タブの粒度**が正しく、
キーバインド側は1回書けば全タブで使い回せる。

```yaml
defaults:
  dir: ~/src/github.com/example/some-repo
jiraSections:
  - title: Other board
    jql: project = OTHER
    dir: ~/src/github.com/example/other-repo   # section が defaults に勝つ
```

これは2通りに効く。コマンド自身の cwd になり、かつ `{{.Dir}}` で参照できる。
両方あるのは、`tmux split-window` / `new-window` が**cwd を継承せず `-c` で受け取る**
ため — 新しいペインやウィンドウを開く類のコマンドには `-c {{.Dir}}` が必要で、
`git log` のような自前で完結するコマンドには cwd だけで足りる。

先頭の `~` は展開する。パスの存在は**起動時に**確認する（キーを押した瞬間ではなく）
— typo が「入れないディレクトリ」についてのコマンド側のエラーとして、ずっと後に
出てくるのを避けるため。

`o`（ブラウザで開く）などは設定の `keybindings.issues` 次第。`{{...}}` はシェル
クォート済みで埋まるので、変数を自分でクォートしてはいけない。設定したキーは
`create` の分も含めて `?` に出る（`name:` があればその名前、無ければコマンド本文）。

`?` はフッターの下に、キー（明色）と説明（暗色）を**列で揃えて**並べる（gh-dash と同じ）。
列数は幅に合わせて 4〜1 で選ばれ、列幅はその列の中身だけで決まる。長いコマンドは
その列に収まる分だけ切る — 1つの長い行のために後続の列を画面外に押し出さないため。

## 打った文字を受け取るキー（`prompt: true`）

`keybindings.issues` のキーに `prompt: true` を付けると、そのキーは即実行せず、
下端に枠を出して**打った文字を受け取る**（複数行可、`Ctrl+d` で送信）。空のまま送ると
拒否する — 指示の無い実行は `prompt: true` の無いキーがやることなので。

受け取った文字は2つの変数で渡せる。**どちらを使うかは間違えると外に出る**。

| 変数 | 中身 | 用途 |
|------|------|------|
| `{{.Prompt}}` | 課題キー ＋ タイトル ＋ 本文 ＋ `---` ＋ 打った文字 | Claude に渡す |
| `{{.Input}}` | 打った文字**だけ** | Jira に投稿する |

取り違えると、コメント投稿に `{{.Prompt}}` を使ってタイトルや本文まで Jira に
載ってしまう（なぜ2つに分けたかは [docs/adr/0009](docs/adr/0009-prompt-versus-input.md)）。

### Claude に渡す

```yaml
- key: a
  name: ask claude
  prompt: true
  command: >-
    jhd-claude-split {{.Dir}}
    claude --permission-mode auto {{.Prompt}}
```

ペインで開くのは、Claude と jhd を**同じ画面に並べる**ため（なぜテンプレート
文字列やウィンドウ分割ではなくスクリプトでペインを割るのかは
[docs/adr/0008](docs/adr/0008-claude-pane-budget.md)）。

### `jhd-claude-split`（ペインの割り方）

`scripts/jhd-claude-split` は tmux のペインを**最大2枚まで**開く。3枚目と tmux の
外からの呼び出しは拒否する（理由は [docs/adr/0008](docs/adr/0008-claude-pane-budget.md)）。

```
┌────────────────────────────────┐
│ jira-dash          160x20      │  ← 幅は変わらない
├───────────────┬────────────────┤
│ 1枚目  80x24  │ 2枚目  79x24   │
└───────────────┴────────────────┘
```

- **1枚目**は jhd を下に割る（`-fv -l 24`）。行は減るが桁は減らない
- **2枚目**は jhd ではなく**1枚目**を横に割る。jhd は 160x20 のまま影響を受けない
- **3枚目**は拒否し、tmux のステータス行に理由を出す
- ペインを1枚閉じれば枠が空く（印は tmux のペイン単位ユーザオプション
  `@jhd-claude` で、ペインと一緒に消える）

インストール:

```bash
ln -s "$PWD/scripts/jhd-claude-split" ~/.local/bin/jhd-claude-split
```

本文を同梱するのは、プレビューが**すでに取得済み**だから（渡さないと受け手が
Jira REST 呼び出し（0.5〜1.2s）をもう一度払うことになり、そもそも認証情報を
持っていないかもしれない）。本文が空、または本プログラム自身の言葉である
`*no description*` のときは何も入れない（「説明が無い」という話を Claude に
させないため）。

制約: 本文はプレビューの取得が終わっていないと入らない（デバウンス 150ms ＋
Jira REST の応答待ち）。カーソルを置いた直後に押すとタイトルと指示だけになる。

`prompt: true` の無いキーは今までどおり即実行する — ブラウザで開くような固定の
コマンドにはそちらが正しい形。

### コメントを投稿する（`refresh: true`）

同じ枠で、渡す先を `jira comment add` にすれば投稿になる。ダッシュボード自身が
Jira を書き換えるのは作成だけで、それ以外は設定のコマンドに委ねる方針
（[docs/adr/0006](docs/adr/0006-writes-go-through-keybindings.md)）による。

```yaml
- key: m
  name: comment
  prompt: true
  refresh: true
  command: jira comment add {{.IssueKey}} -b {{.Input}}
```

`refresh: true` はコマンドが**正常終了したら**行とプレビューの両方を取り直す
（`r` と違って行は消さない）。既定は off — 付けると Jira REST の呼び出しが2回
（約1〜2.4s）増える。

`{{.Prompt}}` で書くとタイトルと本文までコメントとして Jira に載る。投稿系は
`{{.Input}}`。

## 選択肢から選ぶキー（`choices` / `choicesFrom`）

`choices` か `choicesFrom` を付けたキーは、同じ枠に**リストを出して選ばせる**
（`j`/`k` で移動、`enter` で決定、`esc` で取消）。選んだ値が `{{.Choice}}` に入る。
担当者やステータスのように**受け付ける値が短い固定集合**のものは、打つより選ぶほうが
速く、かつ確実 — accountId とサイト固有のステータス名は、誰も記憶から正しく打てない。

`choicesFrom` には3つのソースがある。

```yaml
#: ステータスを変える（今実際に選べる遷移から）
- key: s
  name: status
  choicesFrom: transitions
  refresh: true
  command: jira edit {{.IssueKey}} -S {{.Choice}}

#: 担当者を変える（その課題に割り当て可能なユーザーから）
- key: A
  name: assign
  choicesFrom: assignees
  refresh: true
  command: jira edit {{.IssueKey}} -a {{.Choice}}

#: 固定の一覧から選ぶ（choices — API を呼ばず、いつでも同じ選択肢を出す）
- key: p
  name: priority
  choices:
    - label: 高
      value: High
    - label: 低
      value: Low
  refresh: true
  command: jira edit {{.IssueKey}} --priority {{.Choice}}
```

- **`choicesFrom: transitions`** — その課題で今実際に選べる遷移（`GET
  /issue/{key}/transitions`）。label は付かず、遷移名がそのまま `{{.Choice}}` に渡る
- **`choicesFrom: assignees`** — その課題に割り当て可能なユーザー（`GET
  /user/assignable/search`）。label が表示名、value が accountId
- **`choicesFrom: statuses`** — 設定ではなく**ダッシュボード自身の状態**から候補を作る
  （今のタブの行が持っている status、重複を除いて出現順）。行が無い status は出ない

3つのソースの選定理由（`transitions` の正確さ、`assignees` で accountId を手打ちしな
くていい理由、API を呼ばない `statuses` をあえて残す理由）は
[docs/adr/0010](docs/adr/0010-picker-choice-sources.md)。

`transitions` / `assignees` は API 呼び出しなので、候補が届いてから枠が開く
（フッターに読み込み中と出る）。取得に失敗した場合も枠は開かず、フッターに理由が出る。

固定の一覧が欲しければ（サイトに問い合わせず、いつも同じ選択肢を出したければ）
`choicesFrom` を消して `choices` に列挙する。`label` と `value` が別なのは、まさに
選択肢が要る値でその2つが食い違う場合のため — `label` を省くと `value` がそのまま
表示される。

`prompt` / `choices` / `choicesFrom` を同じキーに2つ以上書くと**起動時に落ちる**。
1つのキーが開けるものは1つで、両方書くと先に見たほうが黙って勝つ。

自分の accountId は `jira auth status` が出す（`choices` に手で書く場合に使う）。

## 課題の作成

キーと課題型の対応は設定で決める。型の名前はサイトごとに違う（日本語サイトなら
日本語）のでコードには置けない。

```yaml
create:
  - key: c
    type: Task
  - key: C
    type: Story
```

このキーで下端に枠が出る（gh-dash の "Approve with comment…" と同じ形）。
`Ctrl+d` で送信、`esc` / `Ctrl+c` で取消 — 枠の中では `enter` が改行になるので、
使えるキーは枠の下端に書いてある。

**打つのはタイトルだけ**。型はキーで決まり、project とスプリントは**カーソル下の行から
継承する** — だから新しい課題は今見ていたものの隣に落ちる。継承するスプリントは active
なもの、無ければ future（= 名前付きバックログ）。closed は選ばない: 課題は過去の全
スプリントを保持するので、リストの最後は現行スプリントではない。

行が無いタブでは開かない（継承元の project が無い）。空タイトルは送らずに拒否する。

入力欄は `create` が1行（`jira create -s` が受けるのは1つの文字列）、`prompt: true`
のキーは3行。枠の高さは**表から引かれる** — 画面の下に足すのではないので、フッターが
画面外に落ちない。

## 設定リファレンス

- **キャッシュ**: セクションの内容は `~/.cache/jira-dash/` にキャッシュされ、まず
  キャッシュから描画してから裏で最新の内容を取り直す。キーはセクションのタイトルで
  はなく JQL なので、タブ名を変えてもキャッシュは生き、JQL を変えれば別キーになる
- **`sprintPrefix`**: セクションにこれを指定すると、JQL で取得した後にスプリント名を
  前方一致で絞り込む。**そのセクションの `limit` は絞り込む前の件数をカバーする値に
  する**必要がある
- **`ORDER BY`**: セクションの JQL には `ORDER BY` を必ず書く。無いと `limit` を超えた
  分がプロジェクト単位で丸ごと落ちることがある
- **書き込み系のキーバインド**: jhd 自身が Jira に書き込むのは課題作成のみ。コメント・
  ステータス・担当者などその他の変更は、すべて設定のキーバインド（シェルコマンド）
  で行う

各設定の背景にある判断は [docs/adr/](docs/adr/) を参照。
