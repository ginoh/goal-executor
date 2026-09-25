# 第1段階のDockerデモ

## 準備

Go 1.26.5、Docker CLI、起動済みDocker Desktopを使用します。Runtimeは常に`desktop-linux` contextを指定します。ビルド元イメージ`golang:1.26.5-alpine`が手元にない場合は、Dockerビルド時にレジストリから取得されます。

通常の`go test ./...`はDockerへ接続しません。サンプル設定は[examples/goal.yaml](../examples/goal.yaml)です。ビルド対象のGoアプリは`cmd/demo-api/main.go`、Dockerfileは`examples/demo/Dockerfile`です。

## 実行

```sh
go run ./cmd/goal-executor run --goal examples/goal.yaml --once
```

空の環境なら`build-image → deploy-app → verify-app → achieved`と進みます。標準出力はJSON Linesで、`planned`に操作列と理由、`observed`に観測状態が出ます。コンテナはlocalhostの動的ポートで公開します。

```sh
docker --context desktop-linux port goal-executor-demo-app 8080/tcp
```

表示された`127.0.0.1:<port>`の`/health`は200、`/message`は`hello`を返します。

同じコマンドを再実行すると、入力IDに対応するイメージと既存コンテナを再利用し、verificationを再実行します。`examples/goal.yaml`の`goal.state`を`ready`にするとreadinessまでを要求し、`verified`へ戻すとverificationを要求します。継続実行する場合は`--once`を外します。

アプリのソースやDockerfileを変更した場合はCLIを再起動してください。次の起動で新しい入力IDを計算し、ビルド・更新します。実行中にアプリ構成や確認方法を変更すると、新しい操作を保留し再起動が必要と通知します。

## 後片付け

実行中のCLIはCtrl-Cで終了します。コンテナの削除は別コマンドです。

```sh
go run ./cmd/goal-executor cleanup --environment demo
```

cleanupは所有ラベルを確認してから対象コンテナを停止・削除します。イメージやビルドキャッシュは残ります。同じ環境名を複数のCLIから同時に操作する用途は対象外です。

## 検証の範囲

`go test ./...`は入力固定、YAML検証、計画、制御ループ、Docker観測の単体テストを実行します。Dockerfileの選択変更、リダイレクトを追跡しないHTTP判定、探索の最短経路・同数時の選択順、失敗時の停止、達成後の目標変更、待機のキャンセルも確認します。

第1段階の完了判定には、以下の5シナリオを使用します。同じ環境を操作するCLIを並行起動しないでください。

### 手動確認の手順

初回は未使用の環境名をYAMLの`environment`に設定します。以下のコマンド例は`demo`なので、変更した場合はコンテナ名・cleanupの環境名も合わせます。各シナリオで`executed`の操作列と最後の`planned`の結果を記録してください。計画に含まれた操作と実際に完了した操作を区別します。

再利用・更新の比較では、各実行の前後で次を記録します。

```sh
docker --context desktop-linux container inspect --format '{{.Id}}|{{.State.StartedAt}}|{{.Image}}' goal-executor-demo-app
```

| シナリオ | 手順 | 期待する結果 |
| --- | --- | --- |
| 1. 空の環境 | ソースの応答と`bodyEquals`を`hello`、目標を`verified`にして`run --goal examples/goal.yaml --once`を実行 | `build-image → deploy-app → verify-app`が完了し、`achieved`になる。DB操作はない |
| 2. 同じ入力の再利用 | 設定・ソースを変更せず同じコマンドを再実行 | `verify-app`だけが完了する。コンテナID・起動時刻・イメージIDは前後で一致する |
| 3. ソース変更後の更新 | CLI終了後に`cmd/demo-api/main.go`の応答を`hello-v2`へ変更し、YAMLの`bodyEquals`も合わせて再実行 | 入力IDが変わり、ビルド・更新・検証を行う。コンテナID・イメージIDが変わり、新しい応答で`achieved`になる |
| 4. 実行中の目標変更 | `goal.state`を`ready`にし、`--once`なしで起動。達成ログを確認してから同じファイルの`goal.state`だけを`verified`へ変更 | `ready`ではverificationを実行しない。変更後に`verify-app`を実行して達成する。コンテナID・起動時刻・イメージIDは変わらない |
| 5. 応答不一致 | 4のCLIをCtrl-Cで終了。ソースはそのまま、YAMLの`bodyEquals`を応答と異なる値にして`--once`で再実行 | ビルド・更新せず検証が失敗し、非ゼロで終了する。`achieved`を出さない |

3以降は現在のソースと確認条件を使い続けます。確認後は自分が変更した応答・`bodyEquals`・`goal.state`・環境名を元の値へ戻し、検証した環境をcleanupします。ソース変更時の更新やcleanupはコンテナを停止・削除します。作成したイメージとビルドキャッシュは残ります。

### 補助的な自動確認

既に用意した次のテストは、初回構築と同じ入力の再利用に範囲を限定した補助確認です。5シナリオ全体の代わりにはせず、追加の自動化は手動確認後の必要性で判断します。明示的に指定した場合だけDockerを操作します。

```sh
GOAL_EXECUTOR_DOCKER_TEST=1 go test ./cmd/goal-executor -run TestDockerScenario -v -count=1
```

このテストは一時コンテナとローカルイメージを作り、終了時にCLIのcleanupでコンテナを削除します。元イメージがない場合はレジストリから取得される可能性があります。

## 2026-09-26の実Docker検証結果

Docker Desktopの`desktop-linux` contextで、上記5シナリオを確認した。途中でサンプルの`.dockerignore`を修正しているため、以下は修正前後の確認を合わせた結果であり、最終構成で5シナリオすべてを通し直した記録ではない。補助的な`TestDockerScenario`も修正前に成功した。

| シナリオ | 結果 |
| --- | --- |
| 空の環境 | `build-image → deploy-app → verify-app → achieved`。DB操作なし |
| 同じ入力で再実行 | `verify-app`のみ。コンテナID・起動時刻・イメージIDが一致 |
| ソース変更 | `hello`を`hello-v2`に変更すると入力IDが変わり、ビルド・コンテナ更新・検証後に達成。コンテナID・イメージIDも変化 |
| 実行中の目標変更 | `ready → verified`で`verify-app`のみ実行し、コンテナID・起動時刻・イメージIDは維持 |
| HTTP応答不一致 | `verify-app`が失敗し非ゼロで終了。`achieved`は出ず、コンテナID・起動時刻・イメージIDも維持 |

途中、サンプルの`.dockerignore`が`examples/goal.yaml`を取り込んでおり、CLIを再起動して目標だけを変えたときに再ビルドした。`examples/goal.yaml`をビルド入力から除外して修正し、回帰テストを追加した。修正後、CLIを再起動して`verified → ready → verified`と切り替えても入力IDが一致し、ビルド・コンテナ更新は発生しなかった。

セルフチェック後、修正済みの構成でソース変更後の更新を追加確認した。既存イメージを使って`hello`を返すコンテナを起動・検証した後、ソースの応答とYAMLの期待値を`hello-v3`へ変更した。YAMLはビルド入力から除外された状態で、`build-image → deploy-app → verify-app`の各`executed`と最後の`achieved`を確認した。比較値は以下のとおり（IDは先頭12文字）。

| 項目 | 更新前 | 更新後 |
| --- | --- | --- |
| 入力ID | `7e4df03fe666` | `8cad46a395bd` |
| コンテナID | `18709bf96fa6` | `163e222a0a1c` |
| イメージID | `bdf5a6b0937b` | `a06ffbb822a7` |
| 起動時刻（UTC） | `2026-09-25T16:23:04.058856711Z` | `2026-09-25T16:23:25.356129221Z` |

HTTP応答不一致の確認も`.dockerignore`修正後に実施した。継続実行中の`ready → verified`は修正前の確認であり、修正後の目標切り替え確認はCLIを起動し直して実施したものとして区別する。

最初のDockerビルドはSandboxが`~/.docker/buildx/.lock`と`~/.docker/contexts/meta`へのアクセスを許可しておらず失敗した。対象ディレクトリへのturn限定の権限を追加して再実行したところ成功した。Dockerfileの診断で作成した`goal-executor-stage1-diagnostic:local`と検証で作成したイメージは残る。サンプルのソースとYAMLは元の内容に戻し、`demo`コンテナはcleanup済み。

入力IDはローカルのビルドファイルを識別します。ベースイメージやビルド中に取得する外部依存まで固定する再現可能ビルドではありません。verification成功はコンテナID・起動時刻・チェック内容に結び付けたメモリ上の記録で、CLI再起動後は確認し直します。
