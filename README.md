# goal-executor

アプリ構成と目標から、ローカルDockerで必要なビルド・起動・検証を計画して実行するPoCです。目標と観測状態を比較し、1操作ごとに再観測・再計画します。

現在の実装は[アプリ構成と目標からの実行の第1段階](docs/application-goals.md)です。DBを使わないGo HTTPアプリを題材に、パイプラインを別途記述せずに、アプリのDockerfileとYAMLの構成・確認方法・目標から実行できるかを試します。第2段階のPostgreSQL対応は未実装です。

## 試し方

Go 1.26.5とDocker Desktopを使用します。Docker Runtimeは`desktop-linux` contextを指定します。[Dockerデモ手順](docs/demo.md)に準備・実行・後片付けを記載しています。

```sh
go test ./...
go vet ./...
go run ./cmd/goal-executor run --goal examples/goal.yaml --once
go run ./cmd/goal-executor cleanup --environment demo
```

`examples/goal.yaml`はアプリのビルド元・ポート、readiness・verificationのHTTP確認方法、`ready`または`verified`という目標を分けて記述します。起動時にビルド入力を一時コピーして固定し、同じコピーから入力IDを計算してDockerへ渡します。同じ入力のローカルイメージと設定が一致するコンテナは再利用します。

実行中は`goal.state`のみ変更できます。ソース・アプリ構成・確認方法を変えたらCLIを再起動します。CLIは終了時にコンテナを残し、`cleanup`で明示的に片付けます。イメージとビルドキャッシュは削除しません。

## ドキュメント

| 文書 | 役割・対象時点 |
| --- | --- |
| [Dockerデモ](docs/demo.md) | 現行実装の準備・実行・後片付けと、実Docker検証の結果 |
| [アプリ構成と目標からの実行](docs/application-goals.md) | 現行の第1段階の仕様・検証方針と、未実装の第2段階の計画 |
| [最初のAPI＋DB PoC](docs/initial-poc.md) | 2026-09-23時点の構想・実装記録。現行仕様とは区別して参照 |
| [設計上の問い](docs/questions.md) | 長期的な探索項目。実装予定を確約するものではない |
