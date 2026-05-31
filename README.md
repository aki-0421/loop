<h1 align="center">loop</h1>

<p align="center">AI エージェントに、リポジトリを読み、実装し、PR まで進めてもらうための Git ネイティブな開発ハーネス。</p>

<p align="center">
  <a href="#quick-start">はじめる</a> |
  <a href="#model">仕組み</a> |
  <a href="#docs">ドキュメント</a> |
  <a href="README.en.md">English</a>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/aki-0421/loop"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/aki-0421/loop.svg"></a>
</p>

<p align="center">
  <img src="assets/loop.png" alt="loop cover" width="800">
</p>

`loop` は、AI エージェントを「次に何をすべきか」から任せて走らせるための CLI です。

チャットで一手ずつ指示する道具ではありません。1 つの Markdown ファイルを起点に、計画、実装、レビューの役割を分けてエージェントを走らせます。1 回のイテレーションは、1 つのレビュー可能でマージ可能な PR です。`loop` が branch と worktree を作り、計画役が依存関係つきの作業ツリーを出し、実装役が分離された worktree で変更を作り、検証を通し、レビュー役が PR 作成、チェック待ち、merge までを `loop` のコマンド経由で進めます。

大事にしているのは、**自走させること** と **管理できる形で残すこと** の両立です。AI には止まらず進んでほしい。一方で、Git 操作、commit、PR、検証、cleanup、監査ログはハーネス側で揃えます。重要な不明点は GitHub Issue に残し、それ以外の進められる作業は止めません。

## どんな人に向いているか

`loop` は、次のような開発スタイルに向いています。

- AI に単発の差分ではなく、リポジトリの文脈を読んで継続的に前進してほしい。
- 自走の単位を、あとから読める PR として残したい。
- 実装だけでなく、検証、PR checks、merge まで任せたい。
- 重要な曖昧さは GitHub Issue に残しつつ、止めずに進めたい。
- 後から「何を読んで、何を計画し、何を変更し、何を検証したか」を追えるようにしたい。

逆に、対話しながら一緒にコードを書く用途や、1 回だけ小さなパッチを作る用途には少し大きい道具です。

<a id="quick-start"></a>
## はじめる

必要なもの:

- 初期化済みの Git リポジトリ
- Go 1.25 以上
- `npx` が使える Node.js/npm 環境
- 対応しているエージェント CLI。既定では `codex exec --json` を使います。
- 既定の PR ワークフロー、GitHub Issues、GitHub を使ったメモリ同期を使う場合は、認証済みの `gh`

インストールします。

```sh
go install github.com/aki-0421/loop/cmd/loop@latest
```

リポジトリで初期化します。

```sh
loop init
```

次に、エージェントへ渡す指示ファイルを書きます。細かな作業手順を書き尽くすよりも、エージェントが読むべき信頼できる情報源を示すのが基本です。

```md
# Instructions

You are acting as a senior coding agent for this repository.

## Direction

Advance the product according to the repository source of truth.

## Source of truth

- Use the specifications and documents under `docs/` as the primary source of truth.
- Check the existing codebase, tests, examples, and nearby repository documents before assuming new behavior.
- Choose one coherent AI sprint-sized PR at a time.
- Keep unrelated sprint goals in separate iterations.

## Coding policy

- Preserve existing architecture and ownership boundaries.
- Add focused validation for the behavior you change.
- Avoid unrelated refactors.
```

最初は 1 イテレーションだけ、人間レビューありで試すのがおすすめです。

```sh
loop run task.md --pr --human-review --max-iterations 1
```

その後、自走させます。

```sh
loop run task.md --pr
```

既定では `run.maxIterations` は `0` です。これはイテレーション数に上限を置かないという意味です。`--goal` を指定しない場合、`loop` はエラー、割り込み、または明示的な上限に達するまで、次のまとまった実装スプリントを選び続けます。

停止条件を明示したい場合は `--goal` を渡します。

```sh
loop run task.md \
  --goal "The authentication tests are implemented and configured validation passes" \
  --pr
```

`--goal` があるときだけ、planner/reviewer の `goal_complete=true` が run 全体の停止条件になります。停止するのは、その goal を満たすイテレーションが正常に統合された後です。

<a id="model"></a>
## 仕組み

`loop` は、AI の判断に任せる部分と、Git/PR 周りを確実に進める部分を分けています。

1. `loop run` が設定を読み、実行状態を記録し、必要なら GitHub context を同期して、イテレーション `0001` を作ります。
2. 計画役のエージェントが実行時の context、instruction、リポジトリのドキュメント、コード、最近の GitHub context を読み、1 つの実装スプリントに対応する task tree を書きます。
3. CLI が task ごとの worktree を作り、依存関係と衝突情報に従って実装役のエージェントを並列実行します。
4. 実装役のエージェントは、編集前に task-local TODO を作ります。各 TODO は `loop task todo complete` で 1 commit になり、完了した task branch は `loop task merge` で iteration branch に squash merge されます。
5. 設定された検証コマンドが iteration worktree で実行されます。
6. レビュー役のエージェントが diff、task tree、task result、検証結果を確認します。PR mode では branch rename、PR title/body 作成、PR 作成、チェック待ち、merge を `loop pr` 経由で行います。
7. 検証失敗やレビュー指摘は repair task になります。
8. merge 後、`loop` は local worktree/branch を cleanup し、結果を記録し、必要なら次のイテレーションを始めます。

エージェントは、`loop` が管理する lifecycle に対して raw `git` や `gh` を直接実行しません。commit、merge、PR、checks、cleanup、監査状態は `loop` のコマンドに集約されます。

## 自走のルール

- `loop run` は既定で完全自動です。
- エージェントはユーザーに直接質問しません。
- 情報が足りないときは、明示的な仮定を置いて進めます。
- 重要なプロダクト、方針、仕様の曖昧さは `loop issue ask` で GitHub Issue にします。
- 確認用の Issue を作った後も、関係のない安全な作業は続けます。
- CLI `--goal` がない run は、終わりを決めずに走るモードです。エージェントの出力だけでは run 全体を完了扱いにできません。
- CLI `--goal` がある run は、その goal を満たすイテレーションが統合された後だけ停止できます。

## コマンド

人間が使う主なコマンド:

| コマンド | 説明 |
| --- | --- |
| `loop init` | リポジトリ用の設定を作り、既定の skill を `npx skills` 経由でインストールします。 |
| `loop run <instruction.md>` | 自走する実装イテレーションを実行します。 |
| `loop version` | ビルドバージョン、commit、date を表示します。 |

エージェント向けのコマンドは簡潔な help にまとまっています。

```sh
loop help agent
```

人間向けの help:

```sh
loop help <command>
```

## 設定

`loop init` は小さな `.loop/config.yaml` を書きます。通常の既定値は組み込みです。

特に重要な既定値:

- Pull request integration が既定のワークフローです。
- `run.maxIterations` は `0` です。つまりイテレーション数に上限を置きません。
- 検証コマンドはリポジトリ側で設定します。

よく使う設定例は [configuration guide](docs/configuration.md) にあります。完全な仕様は [configuration specification](spec/03-configuration.md) を見てください。

## 実行時ファイル

`loop` は、実行の証跡を `.loop/` 以下に保存します。

```text
.loop/config.yaml
.loop/runs/
.loop/worktrees/
.loop/locks/
.loop/loop.db
```

イテレーションの記録には、instruction snapshot、effective config、event logs、task tree、task results、task merge audits、validation evidence、review result、PR state、PR checks、errors、GitHub update summaries が含まれます。

## 開発

開発用コマンドは `make` から実行します。

```sh
make test
make build
make verify
make ci
```

ローカルのスナップショットビルド:

```sh
make release-snapshot
```

<a id="docs"></a>
## ドキュメント

- [English README](README.en.md)
- [Specification index](spec/00-index.md)
- [System contract](spec/01-system-contract.md)
- [CLI contract](spec/02-cli.md)
- [Configuration guide](docs/configuration.md)
- [Configuration specification](spec/03-configuration.md)
- [Iteration workflow](spec/06-iteration-workflow.md)
- [Git and PR workflow](spec/07-git-and-pr.md)
- [Memory and logs](spec/08-memory-and-logs.md)
- [Go implementation notes](spec/13-go-implementation.md)
- [Pull request template](.github/PULL_REQUEST_TEMPLATE.md)
