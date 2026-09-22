# 実行基盤の設計上の問い

更新日: 2026-09-21

[構想とPoC](design.md)を考えるための探索項目。元のテーマ検討で挙がった問いを引き継ぐ。すべてをPoC前に解決したり、すべての機能を実装したりする前提ではない。

## 実行モデル

- Workflowの最小単位は本当にStepでよいか。
- 実行グラフとresource graphは別物か。どう結び付けるか。
- Workflowとstate machineをどう使い分けるか。
- long-running executionをどうモデル化するか。

## 目標状態と手順

- 手順と目標状態をどこまで分離できるか。
- reconciliationとworkflowをどう統合するか。
- failure時は再実行すべきか、再観測・再計画すべきか。
- 目標変更時に、実行中の操作や既存の成果をどう扱うか。
- 目標の達成と、その状態の継続維持をどう区別するか。

## Retry / Rollback

- retryはstepの属性か、execution policyか。
- rollbackは逆向きworkflowか。
- compensationを第一級概念にすべきか。
- 操作の成功報告が得られないとき、実際の副作用をどう確認するか。

## Runtime

- Kubernetes依存にする必要はあるか。
- workerはcontainer / VM / local / serverlessを統一的に扱えるか。
- schedulerの責務をどこまで持たせるか。

## 状態と履歴

- execution historyを監査ログとして扱うか。
- event sourcingを使うべきか。
- replay可能にする価値はどこまであるか。
- observed stateをどの程度保持するか。
- 履歴を調査・分岐・再実行に使える状態として扱うか。

## 人とのやりとり

approval、pause / resume、manual intervention、overrideを第一級概念にすべきか。

## 比較と設計判断

Argo Workflows、Tekton、Temporal、Kubernetes controller / reconciliation modelを主な比較対象として考える。Daggerも実行モデルの比較候補。調査結果は必要に応じて別途記録する。

- それぞれは何をプリミティブとしているか。
- 意図的に扱っていない問題は何か。
- 本システムでは何をcore abstractionにするか。
- 最初の小さな実装で、どの違いを体験できるか。

当初挙がった設計の選択肢は、YAML中心にしない、Kubernetes前提にしない、Step中心にしない、durable execution中心、rollbackの標準化、人の介入の第一級化、resourceとexecutionの統合、目標からの計画生成、調査可能な実行履歴など。

これらは差別化の探索案であり、すべてを採用する制約ではない。現在の中心は目標からの計画生成と、観測・実行の循環。技術選定を先に固定しない。

## 必要に応じた独立実験

reconciliation、scheduler、execution graph、event sourcing、retry semantics、rollback model、distributed lock、benchmark、failure simulationなど。実験の結果は本体の設計判断へ戻す。Labの利用は必要な場合に限る。
