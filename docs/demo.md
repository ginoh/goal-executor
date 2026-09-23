# Dockerデモ

## 準備

Go 1.26.5、Docker CLI、起動済みDocker Desktopを使用します。Runtimeは常に `desktop-linux` contextを指定します。現在選択中のcontextは変更しません。

以下のpull/buildはレジストリへ接続します。実行時の自動pullは行いません。

```sh
docker --context desktop-linux pull postgres:17-alpine
docker --context desktop-linux build -f examples/demo/Dockerfile -t goal-executor-demo:local .
```

APIのビルドには `golang:1.26.5-alpine`、実行には `postgres:17-alpine` を使用します。APIは同梱の `psql` でDBに問い合わせるため、Goの追加依存はありません。これはPoCを小さく保つ選択で、接続プールを持つ一般的なアプリケーション構成ではありません。

## 1. 空の環境から構築

```sh
go run ./cmd/goal-executor run --goal examples/goal.yaml --once
```

`examples/goal.yaml` の環境名は `demo` です。他の実行と重ならない名前を使ってください。同じ環境を複数プロセスで同時に操作する用途は対象外です。

標準出力はJSON Linesです。`planned` の `plan.Actions` に計画全体と理由、`executing` / `executed` に実際に実行した操作、`observed` に観測状態が出ます。空の状態なら次の順に進みます。

```text
create-db → initialize-data → deploy-api(v1) → run-integration-test(v1)
→ planned: achieved
```

毎回計画の先頭だけを実行し、次の観測から計画を作り直します。最後の `observed` では `TestValid: true` を確認できます。

APIのポートを取得し、表示された `127.0.0.1:<port>` の `/data` をブラウザまたはcurlで開けます。

```sh
docker --context desktop-linux port goal-executor-demo-api 8080/tcp
```

応答には `version`、`dataset`、DB初期化時のUUIDである `generation`、DBから取得した `value: "hello from postgres"` が含まれます。

## 2. 既存DBの再利用

上の実行後、YAMLの `apiVersion` を `v2` に変更し、再び `run --goal examples/goal.yaml --once` を実行します。

```text
deploy-api(v2) → run-integration-test(v2) → achieved
```

DB作成とデータ初期化は不要と判断されます。APIの応答ではversionが変わり、generationは同じままです。後述の自動検証では、初期化済みDBだけがある状態からの起動も確認します。

## 3. 操作途中の目標変更

先にデモ環境を片付け、YAMLを `v1` に戻します。

```sh
go run ./cmd/goal-executor cleanup --environment demo
go run ./cmd/goal-executor run --goal examples/goal.yaml --deploy-delay 15s
```

`executing` の `deploy-api` が表示されたら、15秒の待ち時間中にYAMLを `v2` に保存します。実行中のv1操作は完了を待ち、その次に再計画します。

```text
create-db → initialize-data → deploy-api(v1)
→ deploy-api(v2) → run-integration-test(v2) → achieved
```

v1に対するテストは実行されません。`--deploy-delay` はこの変更を試すための待ち時間で、通常は0です。達成後も監視を続けるため、確認後はCtrl-Cで終了します。CLIを終了してから環境を片付けます。

```sh
go run ./cmd/goal-executor cleanup --environment demo
```

## 自動検証

通常の `go test ./...` はDockerを操作しません。次の明示的な指定で、空の環境、初期化済みDBの再利用、操作途中のYAML変更の3ケースを実コンテナで検証します。

```sh
GOAL_EXECUTOR_DOCKER_TEST=1 go test ./cmd/goal-executor -run TestDockerScenarios -v -count=1
```

各ケースは一意な `a-<scenario>-<timestamp>` の環境を作り、終了時にCLIのcleanupで削除します。失敗時もcleanupを試みます。イメージ名を変える場合は `GOAL_EXECUTOR_API_IMAGE` で指定できます。

2026-09-23にDocker Desktop（Server 28.3.2）で3ケースの成功を確認しました。既存DBのケースではコンテナIDと起動時刻の維持、目標変更のケースではv1のテストを実行せずv2のテストで達成することも検証しています。API/DBに永続volumeがないことと、検証後のコンテナ・network削除を確認しました。

初回検証では `--internal` network上のAPIにホスト側ポートが割り当てられずタイムアウトしました。この環境でlocalhostからAPIへアクセスできる専用bridge networkを採用しています。

## 実装の境界

- 対応する目標はAPI `v1` / `v2` とデータセット `sample-v1`。v1/v2は同じバイナリの応答バージョンを切り替えるデモです。
- 環境名は `[a-z][a-z0-9-]{0,39}`。コンテナ・network名は `goal-executor-<environment>-<role>` です。所有者・環境名・役割のラベルを検証し、別所有者の同名リソースは拒否します。
- APIだけをlocalhostの動的ポートで公開し、DBは専用bridge network内でtrust認証を使用します。DBポートは公開しません。ネットワークからの外向き通信は遮断していません。ローカルの一時デモ用です。
- DBデータはtmpfs上に置き、停止すると失われます。永続volumeは作りません。API更新時は旧APIを停止・削除してから起動するため停止時間があります。
- 結合テスト成功はメモリ上に保持し、API/DBのコンテナID・起動時刻・APIバージョン・データセット・初期化世代・値と照合します。CLIを再起動するとテストし直します。
- 操作成功後も実リソースを観測して達成を判定します。HTTP応答不能はAPIが存在するが未準備の状態として扱い、Dockerコマンド失敗は不在とみなさず停止します。
- `--operation-timeout` は既定90秒。タイムアウトやCtrl-Cでもコンテナが残る場合があります。自動rollbackは行わず、確認後にcleanupします。
- cleanupは所有リソースの検証後、コンテナのstop・通常のrm・networkのrmを行います。image、ビルドキャッシュ、volumeは削除しません。
- Dockerエラーは操作種別と終了コードを返します。任意のコンテナ出力をログへ混入させないため、Dockerのstderrはそのまま表示しません。必要に応じて対象を限定した `docker logs --tail 30 <container>` などで調査します。
