# Proposal: TUI コードのモジュール境界

## Status

提案のみ。現時点では実装しない。

## 背景

TUI は当初ルート直下の少数ファイルとして始まったが、現在は検索、非同期 fetch、
description の prefetch、JQL 編集、複数種類の prompt、preview の配置、定期 refresh などを
持つようになった。関心ごとのファイル分割はすでに進んでおり、`fetch.go`、`prompt.go`、
`search.go`、`help.go`、`layout.go` などの名前から処理を探せる。

一方で、それらはすべてルートの `package main` にあり、次の責務が同じ package に並ぶ。

- プロセス起動と flag 処理
- Bubble Tea の状態、message、update、描画
- 設定 schema、default、validation
- disk cache
- keybinding の template 展開と command 実行
- Jira client を TUI 用 interface に合わせる adapter

特に `Model` は画面全体の状態を直接保持し、`Update` は fetch、detail、prompt、command、
キー操作の結果を一か所で処理する。ファイルの所在より、状態と遷移の境界が読み取りにくく
なり始めている。

ただし、Go のディレクトリは単なる整理用 folder ではなく package 境界である。表示、prompt、
fetch をそれぞれ別 package にすると、同じ `Model` を更新するために field や message type を
大量に export することになり、現在より依存関係が複雑になる可能性が高い。

## 目的

- entrypoint、TUI、設定、永続化、外部 command の責務を区別できるようにする
- Bubble Tea の状態遷移を、機能をまたいで追いやすくする
- package 間の依存を一方向に保つ
- 挙動を変えず、小さい review 単位で移行できるようにする

## 目的に含めないもの

- 機能追加や UI 変更
- Jira API client の再設計
- file ごと、画面部品ごとの package 化
- 将来必要になるかもしれない interface の先行導入
- この proposal の merge と同時に移行を開始すること

## 提案する構成

```text
cmd/
├── jira/
│   └── ...
└── jira-dash/
    └── main.go

internal/
├── dashboard/
│   ├── model.go
│   ├── update.go
│   ├── keys.go
│   ├── section.go
│   ├── fetch.go
│   ├── detail.go
│   ├── prompt.go
│   ├── prompt_view.go
│   ├── search.go
│   ├── view.go
│   ├── styles.go
│   ├── layout.go
│   └── help.go
├── config/
│   ├── config.go
│   └── validation.go
├── cache/
│   └── cache.go
├── action/
│   ├── template.go
│   └── command.go
└── jira/
    └── ...
```

依存方向は次に限定する。

```text
cmd/jira-dash
    ↓
internal/dashboard
    ├──→ internal/config
    ├──→ internal/cache
    ├──→ internal/action
    └──→ internal/jira
```

`config`、`cache`、`action` から `dashboard` への参照は作らない。

## TUI は一つの package に保つ

`model`、`view`、`prompt`、`fetch` を別 package にはしない。これらは同じ Bubble Tea model の
状態遷移を構成しており、分離すると次のどれかが必要になる。

- `Model` の内部 field を export する
- 多数の getter、setter、interface を追加する
- Bubble Tea message type を package 間 API にする
- model と view の間に循環依存を作る

そのため、TUI 全体を `internal/dashboard` とし、package 内では現在と同様に file で関心を
分ける。テストも同じ package に置き、非公開の状態遷移を直接検証できる形を維持する。

## Model の substate 化

package 移動だけでは `Model` の責務集中は変わらない。次の段階で関連 field を substate に
まとめる。

```go
type Model struct {
	deps Dependencies

	sections sectionsState
	detail   detailState
	prompt   promptState
	search   searchState
	ui       uiState
}
```

例えば detail の取得状態は一つの型に閉じ込める。

```go
type detailState struct {
	viewport viewport.Model
	key      string
	seq      int

	body        string
	comments    []jira.Comment
	bodyDone    bool
	bodyErr     error
	commentsErr error
}
```

prompt も create、ask、choice の排他的な mode と、その mode が所有する入力状態を一つに
まとめる。boolean が増えるたびに「同時に true になってよい組み合わせ」を考えなくて済むよう、
可能なら enum 相当の `promptMode` を使う。

## Update は入口として残す

Bubble Tea の `Update` 自体は一つのままにし、message の処理を関心ごとの handler に委譲する。

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case fetchedMsg, prefetchLoadedMsg, tickMsg:
		return m.updateFetch(msg)
	case issueLoadedMsg, commentsLoadedMsg, debounceMsg:
		return m.updateDetail(msg)
	case choicesLoadedMsg, createdMsg:
		return m.updatePromptResult(msg)
	case commandRanMsg, copiedMsg:
		return m.updateActionResult(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	default:
		return m, nil
	}
}
```

handler は `internal/dashboard` の非公開 method とする。message type を他 package の API に
しないことが重要である。

## Package ごとの責務

### `cmd/jira-dash`

flag を読み、option を組み立て、`dashboard.Run` を呼ぶだけにする。設定読み込み、credential、
cache、Bubble Tea program の組み立ては、どこまでを `Run` に含めるとテストしやすいかを移動時に
決める。entrypoint に業務ロジックは置かない。

### `internal/dashboard`

Bubble Tea model、message、update、view、layout を所有する。Jira API の具体的な HTTP 実装は
知らず、TUI が必要とする小さい interface を受け取る。

### `internal/config`

YAML schema、default、validation、path 展開を所有する。UI style や dashboard helper には
依存しない。picker source のような設定 vocabulary もここで型または定数として表現する。

### `internal/cache`

section と issue description の disk cache を所有する。最初から interface にせず、テストで
差し替えが必要になった時点で最小の interface を利用側に定義する。

### `internal/action`

`IssueVars`、shell quoting、template 展開、外部 command 実行を所有する。ただし
`tea.ExecProcess` との結合が強い間は、無理に package 化せず `internal/dashboard/action.go` に
留めてもよい。これは必須の分割ではなく、依存を単純に保てる場合だけ行う。

### `internal/jira`

現在の REST client と Jira domain type を引き続き所有する。TUI 都合の表示や Bubble Tea type は
持ち込まない。

## 段階的な移行案

各段階を独立した PR にし、挙動変更と構造変更を混ぜない。

1. `cmd/jira-dash` と `internal/dashboard` を作り、TUI を機械的に移動する
2. `Model` の field を `detailState`、`promptState`、`searchState` へまとめる
3. `Update` の処理本体を関心ごとの handler へ委譲する
4. `internal/config` を抽出する
5. `internal/cache` を抽出する
6. 依存が単純になる場合だけ `internal/action` を抽出する

最初の PR では package 名、import、entrypoint、test の所在だけを変更し、型名、状態表現、挙動を
変えない。substate 化はその後に行う。これにより、移動による failure と設計変更による failure を
分けて調べられる。

## 移行を始める条件

現状の一 package 構成も Go として不自然ではないため、次のいずれかが継続的に起きるまでは
実装を見送ってよい。

- 新機能のたびに `Model` の boolean や sequence field が増える
- `Update` の一つの変更が複数の無関係なテストを壊す
- 設定 validation と描画 helper の依存が増える
- 外部 command 実行を TUI 以外からも使いたくなる
- ルート package の責務を説明するのが難しくなる

逆に、変更箇所を現在の file 名からすぐ特定でき、状態遷移の修正が局所的なままであれば、
package 移動のコストを払わず現状を維持する判断も妥当である。

## 判断

短期的には現在の file 分割を維持する。次に構造上の痛みが顕在化した時は、細かな package 化では
なく、まず `cmd/jira-dash` と `internal/dashboard` という一つのアプリ境界を作る。その後、
`Model` の substate 化と `Update` の委譲を優先し、`config`、`cache`、`action` の抽出は依存が
一方向に保てるものから段階的に行う。
