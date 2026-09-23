# goal-executor

目標と観測状態から実行計画を導き、観測・計画・実行を繰り返して目標へ収束させる実行基盤のPoC。

最初はAPIとDBからなる一時環境を対象に、初期状態に応じた操作の省略と、実行途中の目標変更による再計画を確かめます。Go、ローカルDocker、CLI＋目標ファイルを使う方針です。

Goの状態モデル、Planner、YAML読み込み、制御ループ、CLI、Docker Runtimeを実装しています。操作の前提条件・予測結果から幅優先探索で最小操作数の計画を導き、1操作ごとに目標を読み直して観測・再計画します。PostgreSQLと小さなGo APIを動かす手順は[デモ手順](docs/demo.md)を参照してください。

## ローカルでの検証

Go 1.26.5を使用します。YAML読み込みには `go.yaml.in/yaml/v3 v3.0.5` を使用します。初回のテスト実行時には、依存がキャッシュになければダウンロードされます。

```sh
go test ./...
go vet ./...
```

[`internal/planning`](internal/planning) のテストで、空の環境からの構築、初期化済みDBの再利用、APIバージョン変更後の再テスト、達成済み・到達不能・探索上限の判定を確認できます。探索処理については分岐のある最短経路、同じ長さの経路の選択順、循環の除外も検証します。Docker接続やリソース作成は行いません。

[`internal/goalfile`](internal/goalfile) はYAMLの必須項目・型・未知のキー・重複キーを検証します。[`internal/control`](internal/control) はファイル変更による再計画、不正な入力からの復帰、達成後の待機、観測・操作失敗時の停止を検証します。

## 目標ファイル

[examples/goal.yaml](examples/goal.yaml) がサンプルです。4項目はすべて必須です。

```yaml
environment: demo
apiVersion: v1
dataset: sample-v1
requireIntegrationTest: true
```

文字列3項目は空欄不可、テスト要否はbooleanで指定します。単一のYAMLドキュメントを扱い、aliasやmergeによる値の参照は対象外です。

制御ループはYAMLエラー時に新しい操作を保留し、修正後に再開します。目標達成後も待機し、既定で1秒ごとに読み直します。この間隔は変更可能です。実行中は最初に読み込んだ環境名に固定し、環境名の変更も入力エラーとして保留します。観測・操作失敗、到達不能、探索上限では理由を返して停止します。

CLIは `go run ./cmd/goal-executor run --goal examples/goal.yaml` で起動します。事前にデモ用イメージを準備してください。`--once` を指定すると達成時に終了します。終了時にはリソースを残し、`cleanup --environment demo` で明示的に片付けます。

## ドキュメント

- [構想とPoC・合意した範囲](docs/design.md)
- [設計上の問い](docs/questions.md)
- [Dockerデモ・検証手順と制限](docs/demo.md)

テーマ候補の比較やプロジェクト横断の検討は [systems-hub](https://github.com/ginoh/systems-hub) で管理します。本リポジトリでは詳細設計、実装、検証結果を管理します。
