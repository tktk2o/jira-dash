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
出力差分で比較するスクリプト。旧 CLI がまだ端末に入っている間しか使えない。

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

両方あるのは、2つの用途が正反対のものを欲しがるから。Claude には課題を**含めて**渡し
たいが、コメントの投稿は**本文だけ**を送らないといけない — 課題はすでにコメント先で
あって、それを本文に混ぜると Jira に載ってしまう。

### Claude に渡す

```yaml
- key: a
  name: ask claude
  prompt: true
  command: >-
    jhd-claude-split {{.Dir}}
    claude --permission-mode auto {{.Prompt}}
```

ペインで開くのは、Claude と jhd を**同じ画面に並べる**ため。あの枠の中で Claude を
動かすことはできない — Claude Code は alternate screen を使う全画面 TUI なので、枠に
収めるには jhd 自身が PTY を持ってターミナルエミュレータになる必要があり、かつ3行の
枠は Claude が使える画面ではない。ペインなら tmux にその仕事をさせられる。

### `jhd-claude-split`（ペインの割り方）

`scripts/jhd-claude-split` は tmux のペインを**最大2枚まで**開く。

```
┌────────────────────────────────┐
│ jira-dash          160x20      │  ← 幅は変わらない
├───────────────┬────────────────┤
│ 1枚目  80x24  │ 2枚目  79x24   │
└───────────────┴────────────────┘
```

- **1枚目**は jhd を下に割る（`-fv -l 24`）。**行は減るが桁は減らない**ので、
  プレビューもヘルプの列レイアウトも生き残る。Claude Code 側は composer と
  ステータス行で数行を先に使うので、狭いと返答がすぐ流れる — 譲るのは jhd の側
- **2枚目**は jhd ではなく**1枚目**を横に割る。だから jhd は 160x20 のまま**一切
  影響を受けない**
- **3枚目**は拒否し、tmux のステータス行に理由を出す。それ以上割ると jhd が2行に
  なり、ダッシュボードとして機能しなくなる（実測: `-v` の素の連続実行で
  45→22→11→5→2 行）
- ペインを1枚閉じれば枠が空く。印は tmux のペイン単位ユーザオプション
  （`@jhd-claude`）なので**ペインと一緒に消える** — 管理する状態を持たない
- **tmux の外では拒否する**。`tmux split-window` は `$TMUX` が無くても
  *成功してしまい*、動いているサーバの無関係なウィンドウを割る（実測）。
  フォールバックではなく拒否が正しい

なぜスクリプトかというと、`-v` と `-h` の選択には既に開いている枚数を知る必要があり、
拒否には理由を言う場所が必要で、どちらもテンプレート文字列に収まらない。そして
どちらも jira-dash に置くべきものではない（jira-dash は tmux の存在を知らない）。

インストール:

```bash
ln -s "$PWD/scripts/jhd-claude-split" ~/.local/bin/jhd-claude-split
```

本文を同梱するのは、プレビューが**すでに取得済み**だから。渡さないと受け手が
Jira REST に 0.5〜1.2s かけてもう一度取りに行くことになり、そもそも認証情報を
持っていないかもしれない。本文が空、または本プログラム自身の言葉である
`*no description*` のときは何も入れない（「説明が無い」という話を Claude に
させないため）。

制約: 本文はプレビューの取得が終わっていないと入らない（デバウンス 150ms ＋
Jira REST の応答待ち）。カーソルを置いた直後に押すとタイトルと指示だけになる。

`prompt: true` の無いキーは今までどおり即実行する — ブラウザで開くような固定の
コマンドにはそちらが正しい形。

### コメントを投稿する（`refresh: true`）

同じ枠で、渡す先を `jira comment add` にすれば投稿になる。ダッシュボード自身が
Jira を書き換えるのは作成だけで、それ以外は設定のコマンドに委ねる — この方針の
ままコメントが増える。

```yaml
- key: m
  name: comment
  prompt: true
  refresh: true
  command: jira comment add {{.IssueKey}} -b {{.Input}}
```

`refresh: true` はコマンドが**正常終了したら**行とプレビューを取り直す。両方なのは、
ステータスと担当者は表の列に出て、コメントはプレビューに出て、jhd 側からはその区別が
つかないから。`r` と違って行は消さない — これは自分で起こした変更の取り直しであって、
待って見るためのリロードではない。無いと投稿した
コメントは、カーソルが行を離れて戻ってくるまで出てこない — それは投稿に失敗したのと
見分けがつかない。既定で off なのは、jhd 側からは「書き込むコマンド」と「ブラウザを
開くコマンド」の区別がつかず、無駄な取り直しは Jira REST の呼び出し2回（約 1〜2.4s）
だから。

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
  /issue/{key}/transitions`）。ワークフローが許す遷移しか出ないので、行の status か
  ら作る `statuses` より正確。label は付かず、遷移名がそのまま表示され、そのまま
  `{{.Choice}}` に渡る — `jira edit -S <名前>` が内部で同じ一覧を引いて名前照合するの
  で、名前がそのまま送る形として合っている。
- **`choicesFrom: assignees`** — その課題に割り当て可能なユーザー（`GET
  /user/assignable/search`）。label が表示名、value が accountId — `jira edit -a`
  は accountId をそのまま送るだけで表示名を解決しないので、accountId を手で調べて
  設定に貼る作業がここで無くなる。
- **`choicesFrom: statuses`** — 設定ではなく**ダッシュボード自身の状態**から候補を作
  る（今のタブの行が持っている status、重複を除いて出現順）。**唯一 API を呼ばない
  ソース**なので、オフラインでも開けるのはこれだけ。限界も明確: 行が無い status は出
  ない — 誰も完了していないタブに「完了」は出てこない。

`transitions` / `assignees` は API 呼び出しなので、キーを押した瞬間には枠は開かない
（フッターに読み込み中と出る）。候補が届いてから開く — 候補ゼロの枠は壊れたキーに
見えるので、届く前に開くことはしない。取得に失敗した場合も枠は開かず、フッターに
理由が出る。

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

## 設計上の判断

- **認証情報ストアはひとつ**: jira-dash は `internal/jira` で Jira REST API を直接
  in-process で叩くが、認証情報は `~/.config/jira-cli/credentials.json`（または
  `JIRA_*` 環境変数）を本リポジトリの `cmd/jira`（`jira auth login`）と共有する。
  もともと外部の TypeScript 製 `jira` CLI に検索・詳細取得・作成を経由させていたのも
  同じ理由（ストアを2つ持ちたくない）だったが、そちらは Go 版の自前 CLI 兼 API クラ
  イアントに置き換わっている。`Searcher` インターフェースは差し替えられるようにして
  ある
- **キャッシュ前提**: Jira REST の1回の呼び出しは、Jira 側のサーバレイテンシが支配的
  で 0.5〜1.2s かかる（実測）。セクションはまずキャッシュから描画し、裏で更新する。
  キャッシュは `~/.cache/jira-dash/`、キーは**タイトルではなくクエリ**なので、タブ名
  を変えてもキャッシュは生き、JQL を変えれば自然に別キーになる
- **`sprintPrefix`**: ボードが複数チームのスプリントを同時に走らせていると JQL では
  分けられない（`sprint` に LIKE 相当が無く、`sprint ~ "Team"` は15件中2件しか返さな
  かった）。かつ active スプリントは毎イテレーション改名されるので、名前の完全一致も
  使えない。前方一致は取得後に適用するので、**そのセクションの limit は絞る前の件数を
  覆う必要がある**
- **`ORDER BY` は必ず書く**: Jira の暗黙の並び順はプロジェクト単位なので、総数が limit
  を超えるとプロジェクト丸ごとが下に沈んで消える（実測: 63件のセクションで、あるプロ
  ジェクトの課題が全部 52 行目以降だった）
- **書き込みは1箇所だけ**: jhd 自身が Jira を変更する経路は作成だけ。コメントの投稿を
  含め、それ以外の変更は設定のキーバインド（シェルコマンド）に委ねる。だから
  コメント・ステータス・担当者は `{{.Input}}` / `{{.Choice}}` / `choices` / `refresh:`
  という**枠と変数**の追加だけで済み、Jira を叩くコードは1行も増えていない
- **アイコンは全て2セル幅**: 1セルの絵文字を混ぜると課題タイプによって右の列がずれる。
  `⚙️`（異体字セレクタ付き）と `🗂` は runewidth で1セルと計算される
