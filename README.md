# Hoopa

Hoopa 是面向终端、Linux SSH 与 Agent 工作流的 **CLI-first 代理管理器**。本地掌握订阅、稳定逻辑组、顺序规则和应用入口，独立管理外部 Mihomo 核心。

当前是 **DB-free P1 的阶段源码快照**，整理上传后继续开发：截至 2026-10-29，完成 **72/140** 项任务，P1 与全项目均未完成。已有 Linux/amd64 的有限模块和实际 CLI 验证，没有正式全平台支持或性能达标声明。T058 已完成受控本地首次旅程，**真实 Agent 外部请求仍未验证**。

## 当前可用入口

| 命令 | 当前边界 |
| --- | --- |
| `init --mihomo PATH --input PATH …` | 显式准备草稿、身份与声明入口；不启动服务、监听代理或 Apply |
| `subscription list` / `subscription update ID` | 只读查询与显式持久更新；不自动应用新快照 |
| `config plan --input PATH --output PATH` | 只读生成 Plan，并写入新的私有输出文件 |
| `config apply --plan PATH --confirm DIGEST --accept-disruption` | 确认精确 Plan 后应用；不会自动重新规划或启动管理服务 |
| `start --control-only` / `service run` | 显式启动管理服务；control-only 不代表已有可用代理配置 |
| `status` / `doctor` / `operation get ID` / `operation recover ID` | 状态与诊断查询、单独显式恢复；查询不代替恢复 |
| `run --app ID [--mode managed\|inherit\|direct] -- ARGV...` / `app inspect ID` | 子进程接入与只读检查；managed 要求已 Apply 且可验证的入口，direct/inherit 可用已初始化草稿 |
| `stop --accept-disruption` / `service stop` | 分别停止所属核心与管理服务；`service stop` 不等于停止核心 |

不默认启用 TUN、系统代理、自启动或后台计划任务。模式仅作用于目标子进程，`direct` 不承诺绕过已有 TUN/VPN。计量、数据库和 TUI 不是当前基础路径的依赖；P2/P3 尚未完成。

## 构建与外部核心

以下面向当前 Linux/amd64 路径，使用固定 **Go 1.27.3**；源码入口为 [`cmd/hoopa`](cmd/hoopa)。以下仅构建 CLI 并执行只读诊断，不初始化或启动代理：

```sh
GOTOOLCHAIN=local go version
mkdir -p private/bin
GOTOOLCHAIN=local go build -o private/bin/hoopa ./cmd/hoopa
private/bin/hoopa doctor
```

Go 依赖由 [`go.mod`](go.mod) / [`go.sum`](go.sum) 固定。缺失或不完整的状态会在 doctor 中明确显示，不能据此认为代理已配置好。上传候选的干净构建复核状态见[源码快照说明](docs/upload-snapshot.md)。

**构建 Hoopa 不会附带一个可替代使用的 stock Mihomo。** 当前运行路径依赖独立批准的核心制品，按完整二进制身份、协议和能力核验；同名文件或相同版本号不足以满足要求。应用入口使用[有限 app-routing 核心批准记录](experiments/mihomo-events/approved-app-core-v2.json)，来源配置另有[来源核心批准记录](experiments/mihomo-events/approved-sources-core-v2.json)。外部核心二进制不进入本源码快照；源码基线、文本补丁与制品摘要见[应用核心 manifest](experiments/mihomo-events/core-app-ordered-v2-manifest.json)。不能把默认打印的 `1.10.0` 当成上游源码版本或完整能力证明。

## 从哪里继续

- **使用与接口**：[本地 init](specs/001-hoopa-proxy-manager/contracts/local-init-v1.md)、[最小配置 CLI](specs/001-hoopa-proxy-manager/contracts/minimal-cli-v1.md)、[应用启动](specs/001-hoopa-proxy-manager/contracts/application-launch-v1.md)、[服务生命周期](specs/001-hoopa-proxy-manager/contracts/service-lifecycle-v1.md)。整体[CLI 规划](specs/001-hoopa-proxy-manager/contracts/cli.md)还包含未交付命令，应以专项契约与验收为准。
- **当前实际验证**：[订阅与双来源](docs/validation/us3-sources.md)、[应用规则](docs/validation/us4-routing.md)、[应用启动/TTY/信号](docs/validation/us2-application-launch.md)、[本地首次配置与 T058 缺口](docs/validation/us2-onboarding.md)。通过项均限定到各自制品、环境和场景。
- **设计与开发**：[功能](docs/features.md)、[架构](docs/design.md)、[路由](docs/routing.md)、[开发规范](docs/development.md)、[上传快照边界](docs/upload-snapshot.md)。
- **历史与失败**：[任务记录](docs/task-log.md)、[任务清单](specs/001-hoopa-proxy-manager/tasks.md)及 [各阶段 validation 记录](docs/validation)保留原型迭代、旧失败与未验证项。新批次通过不会抹掉旧失败。

Git 中保留源码、测试、完整 [`specs`](specs/001-hoopa-proxy-manager/spec.md) 工作材料（spec/plan/tasks/contracts/research）、[`.specify`](.specify) 宪章/模板/脚本、共享 [`.agents` skills](.agents/skills)、ADR、文本补丁、正式验收结论与必要的批准/构建身份记录；逐轮运行报告、安装回执、外部核心、VM 镜像、私有运行原件、凭据及本机状态不随源码上传。Hoopa 原创管理器使用 [MIT License](LICENSE)，上游材料与依赖保留各自许可，见[第三方来源与许可](THIRD_PARTY_NOTICES.md)。

运行报告与复核过程材料保留在本地原位置，不随 Git 同步。文中的“本地原件”代码路径不是 clone 中的可点击文件；正式 acceptance、核心批准、构建 manifest、锁与 fixture 保留原字节，但受控 gate 复核仍需完整的本地报告与私有原件。缺材料不能变为验证通过，既有失败、结果与 hash 不改。
