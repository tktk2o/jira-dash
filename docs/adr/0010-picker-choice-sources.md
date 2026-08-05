# 0010. `choicesFrom` を3つのソースに分ける

## 文脈

担当者やステータスのように受け付ける値が短い固定集合のものは、打つより選ぶ方が
速く確実である（accountId やサイト固有のステータス名は誰も記憶から正しく打てない）。
候補の取得先はソースによって性質が異なる。

## 決定

`choicesFrom` に `transitions` / `assignees` / `statuses` の3つのソースを持たせる。
固定の一覧が欲しい場合は `choicesFrom` の代わりに `choices` を直接列挙する。

## 理由

- **`transitions`**（`GET /issue/{key}/transitions`）は、その課題で今実際に選べる
  遷移だけを返す。ワークフローが許す遷移しか出ないので、行の status から作る
  `statuses` より正確
- **`assignees`**（`GET /user/assignable/search`）は、その課題に割り当て可能な
  ユーザーを返す。label が表示名、value が accountId — `jira edit -a` は
  accountId をそのまま送るだけで表示名を解決しないため、accountId を手で調べて
  設定に貼る作業がこれで無くなる
- **`statuses`** は設定ではなくダッシュボード自身の状態（今のタブの行が持っている
  status を重複除去・出現順で集めたもの）から候補を作る。3つの中で**唯一 API を
  呼ばないソース**であり、オフラインでも開けるのはこれだけ。この利点があるため、
  `transitions` を追加したあとも `statuses` を残した。弱点は明確で、行が無い
  status は出ない（誰も完了していないタブに「完了」は出てこない）
- **遷移は名前で受け渡す**: `transitions` の候補には label が付かず、遷移名が
  そのまま `{{.Choice}}` に渡る。`jira edit -S <名前>` が内部で同じ一覧を id では
  なく名前で照合するため、名前をそのまま送る形が実装と合っている

## 結果（トレードオフ・限界）

- `transitions` / `assignees` はキーを押した瞬間には枠が開かない。API 呼び出しの
  応答を待ってから開く（候補ゼロの枠は壊れたキーに見えるため）
- `statuses` はオフラインで使える代わりに、行に出現していない status を候補に
  出せない
