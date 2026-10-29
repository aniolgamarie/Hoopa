# T054：显式本地初始化

2026-10-29，Linux/amd64 的 `hoopa init` 已完成限定验收。初始化保存完整声明草稿，准备独立应用启动历史的身份和锁文件，并按显式端口准备不可变的控制/共享入口及应用入口凭据。它不下载或执行依赖，不监听端口，不启动服务或核心，不应用配置，也不建立流量账本。

接口和持久化边界见 [local-init-v1](../../specs/001-hoopa-proxy-manager/contracts/local-init-v1.md) 与 [application-launch-store-v1](../../specs/001-hoopa-proxy-manager/contracts/application-launch-store-v1.md)。只完成这些本地准备不表示代理可用；应用 `run`、启动记录的逐阶段写入和完整首次使用流程仍由 T055–T058 实现、验收。

## 使用入口

```sh
hoopa init --mihomo /absolute/path/to/mihomo
```

没有完整 Intent 时，这条命令只返回缺少 `intent` 的 JSON 报告，不创建状态。输入中缺少二进制版本或摘要时同样报告缺少项；不会运行未知二进制猜测版本，也不会持久化占位值。依赖检查只比对所读文件字节和声明摘要，不能代替 Plan 阶段的核心能力验证。

已有完整、显式的 JSON/YAML Intent 时可准备草稿及端口。例如，声明中已包含 `app-a` 这个 managed、未开启计量的应用：

```sh
hoopa init --mihomo /absolute/path/to/mihomo --input ./intent.yaml \
  --controller-port 32000 --shared-port 32001 --app-port app-a=32002
```

这些端口都是用户声明，不会通过扫描自动选择。没有提供端口时，可以保存完整草稿及启动历史身份，同时报告尚缺设置/入口；direct/inherit 草稿不要求当前核心可用。计量仍会明确报告后端未实现。源输入文件保持不变，受保护草稿不改变 active revision、generation 或运行意图。

重复初始化复用已有凭据与入口；显式更换不可变端口、占用其他保留入口端口或管理服务正在持有写锁时返回冲突。已有草稿或启动历史损坏时先拒绝，再进行任何新凭据准备。写入途中失败可能留下已经完成的本地准备，需要下一次显式初始化重新校验；不会报告已回滚、删除历史或重新生成原身份。严格 umask 导致新文件不满足0600时，发布前拒绝。

需要让已准备的 managed 配置生效，仍须使用独立的控制服务与完整 Plan/Apply：

```sh
hoopa start --control-only
hoopa config plan --input ./intent.yaml --output ./plan.json
# 阅读 plan.json；使用其中的实际摘要明确确认，按影响预览提供中断授权。
hoopa config apply --plan ./plan.json --confirm ACTUAL_PLAN_DIGEST --accept-disruption
```

这组命令的真实转发、失败回滚等证据属于 [T139 最小 CLI](minimal-cli.md)，本次 init 验收没有重新执行该代理运行矩阵。计划摘要不是示例占位符可以替代的输入。`status`、`doctor` 和未来 `run` 不会顺手初始化缺失空间。

## 本次证据

| 范围 | 普通 / race 结果 |
| --- | --- |
| 草稿、历史身份、路径隔离、发布故障和隔离 umask 子进程 | 各13顶层、131含子测试 PASS，0失败/跳过 |
| 本地初始化与既有来源身份连续性回归 | 各15顶层、46含子测试 PASS，0失败/跳过 |
| 纯输入验证 | 各6顶层 PASS，0失败/跳过 |
| CLI 参数、缺输入报告、离线草稿与写锁冲突 | 各3顶层 PASS，0失败/跳过 |
| 真实 cmd/hoopa | 各8次调用，预期成功/拒绝全部符合；不是8个代理转发场景 |

真实 CLI 检查包括：缺输入不建状态、非法端口拒绝、完整准备、完全相同的重复调用、端口冲突、重复应用端口参数、写锁冲突、只读 doctor。最后仍无已应用 generation，bundles/operations/runs 为空，runtime 目录为空，原 Intent 和核心字节未改变；启动历史仅有身份与分配锁两个元数据文件。初始化为 JSON 本地报告，不增加管理 RPC；成功的本地报告允许 null request ID，已有 RPC 严格校验不变。

最终源码/fixture共338份冻结输入未变，三个受影响包的 vet 通过。真实普通 CLI SHA256为 `051f9a1633723452f449e5ee4d41b3e316243da3f41f49c5ce8f16a06af13709`，race CLI为 `5ec8742a33e03e6ae73728c180311b563f0e44c0ef6bef7247cb5d55f88b631e`。原件保存在忽略目录 `private/us2-preparation/t054/`；公共摘要见 验收索引（本地原件 `../../experiments/gost-mihomo/us2-local-init-validation.json`，不随Git上传）。二进制不加入 Git 源码文件。

首轮控制层测试执行目录错误造成的 fixture 缺失、CLI 初次 null 成功报告退出码错误，以及两轮独审发现的 umask/历史预检边界均保留原日志和修前审查。修后按受影响路径补验。故障注入不是断电实验；本次不验证应用 exec、TTY、连接、后代状态、计量数据库、UDP/QUIC、其他平台或性能预算。
