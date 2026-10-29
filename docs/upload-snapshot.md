# 当前源码快照与上传边界

本页说明 2026-10-29 准备上传的阶段源码内容，**不表示已 commit、push、发布版本或完成全量验收**。当前仍是 DB-free P1，任务完成72/140；P1、P2/P3和完整项目均未完成。这是保存当前进度、便于继续开发的阶段快照，不终止后续工作。

## 快照内容

准备保留：

- `cmd/hoopa`、`internal/`、`protocol/` 的实现与测试，以及合成 `testdata`／集成夹具。
- `go.mod/go.sum`、设计文档和 ADR。
- [第三方来源清单](../THIRD_PARTY_NOTICES.md)和 `LICENSES/` 中逐字保留的上游许可证、NOTICE。
- 完整 [`specs/`](../specs/001-hoopa-proxy-manager/spec.md) 工作材料：spec、plan、tasks、contracts/schema、research及同阶段配套资料，保留继续实施所需上下文。
- [`.specify/`](../.specify) 的宪章、模板和脚本，以及共享 [`.agents/skills/`](../.agents/skills)；它们是后续规格与协作工作的一部分。
- `experiments/` 中的实验源码、文本补丁、正式构建 manifest/锁、candidate/fixture、核心批准与正式 decision；六份最终 `*-acceptance.json` 保持原字节。
- 当前及历史验收文档，包括失败、修订和未验证边界；通过新批次不删除旧结论。

不随源码上传：逐轮 `*-validation.json`、准备/诊断/test-definition 记录、旧 summary JSON、安装回执、外部核心可执行文件、CLI/test 二进制、VM/kernel/rootfs 镜像、私有原始日志/归档、凭据、订阅秘密、实际状态目录或本机缓存。现有[忽略规则](../.gitignore)表达这条分离边界，但不能替代上传前对候选文件的审查。 BPF/object、编译库、临时备份和补丁拒绝文件同样外置，避免未来中间产物进入候选。私有证据路径只说明原件归属，不是干净 clone 中必须存在的文件；机器上的绝对安装/运行路径不应被当成可移植默认值。

## 可读、可构建和可运行是不同条件

CLI 构建入口是 [`cmd/hoopa`](../cmd/hoopa)，固定 Go1.27.3 与清单中的精确 Go 依赖。构建方法见 [README](../README.md)；工程检查范围见[开发规范](development.md)。

运行已提交来源/应用配置还需要独立提供与批准记录一致的外部核心。当前[基础](../experiments/mihomo-events/approved-basic-core-v1.json)、[来源](../experiments/mihomo-events/approved-sources-core-v2.json)、[应用](../experiments/mihomo-events/approved-app-core-v2.json)是各自有界的能力档案，不是按文件名或通用 Mihomo 版本号自动互换。应用核心的源码基线、文本补丁及构建身份见[对应 manifest](../experiments/mihomo-events/core-app-ordered-v2-manifest.json)。Git 快照不携带这些 ELF，也不将 stock Mihomo 升格为已批准核心。

真实核心、权限、`/proc`/pidfd、loopback、PTY 或隔离 VM 相关测试，需要其各自登记的制品和环境。缺少它们时，应记录未执行/环境不足，不把跳过或只有编译成功算作真实矩阵通过。

## 已有验证与本次准备状态

| 项目 | 已有结论／本次状态 |
| --- | --- |
| 文件状态、Plan/Apply、生命周期及恢复 | 已有分批有限验收，见 [US5 汇总](validation/us5-lifecycle.md)；不合并为一轮全库测试 |
| 双来源、稳定组、故障与持久更新 | 已有实际有限矩阵，见 [US3](validation/us3-sources.md)及[更新模块](validation/us3-source-update.md) |
| 应用入口与顺序路由 | 已有有限实际矩阵，见 [US4](validation/us4-routing.md) |
| 应用 run、IO、TTY、信号、后台后代 | 普通/race各五场有限验证，旧观察器失败保留，见[应用启动](validation/us2-application-launch.md) |
| 从空状态 init→plan/apply→run | 普通/race受控本地 HTTP 旅程已通过，见[T058进度](validation/us2-onboarding.md)；真实 Agent 外部请求未验证，T058仍未完成 |
| 本次上传候选的干净目录构建／检查 | 从 Git 可见文件导出独立目录，固定 Go1.27.3 离线构建 `cmd/hoopa` 和 `go vet ./...` 均通过；默认测试结果及限制见下文 |
| commit / push | 尚未执行；本次只整理可审查的源码与上传边界 |

完整历史和任务状态仍以[任务记录](task-log.md)、[任务清单](../specs/001-hoopa-proxy-manager/tasks.md)及各 validation 文档为准。当前没有正式全平台支持、完整计量/数据库/TUI或性能预算达标声明。

### 历史候选检查：收紧前

前轮候选为 1106 个文件、约 15.8 MB，由 `git ls-files --cached --others --exclude-standard` 导出，包含未跟踪源码，不包含 `private/`、外部核心或本机状态。此数值描述当时快照，不是本轮收紧后的最终清单。这是工作树候选验证，不是远端 clone 或已提交版本的验证。源码、测试、协议和公开验收输入在该次检查期间未修改。

使用已有固定 Go1.27.3 与模块缓存，`GOTOOLCHAIN=local`、`GOPROXY=off` 离线执行；检查命令如下。构建临时目录和缓存可位于本机私有存储，测试运行目录必须满足现有路径检查。

```sh
go build -p=2 -trimpath -buildvcs=false -o /PRIVATE_OUTPUT/hoopa ./cmd/hoopa
go vet -p=2 ./...
test_runtime=$(mktemp -d /tmp/hoopa-upload-tests-XXXXXX)
go test -p=2 -count=1 -timeout=240s \
  -exec="/usr/bin/env GOTMPDIR=$test_runtime" -json ./...
```

`/PRIVATE_OUTPUT` 是显式占位，应替换为自己的私有输出目录。Go 构建与 vet 的退出码均为 0；默认测试退出码为 1，不能记录为全库通过：

- 32 个包中，25 个通过，3 个失败，4 个无测试文件；按顶层测试统计为 979 通过、18 失败、37 跳过。含子测试的事件计数为 5581 通过、19 失败、42 跳过，不与顶层计数相加。
- 失败的 18 个顶层测试中，17 个明确要求 `HOOPA_P1_SOURCES_CORE` 指向实际批准核心，1 个要求本机私有核心审查原件。它们不能仅凭公开源码快照复验；未复制私有材料来绕过上传边界，也未将这些断言改成自动跳过。
- 37 个顶层跳过项保留其原有环境/制品门槛；带额外 build tags 的真实核心矩阵未在本次执行。已有真实验收仍按上表各自记录解释。
- 首次测试运行误将 Go1.27.3 的 `GOTMPDIR` 设置到本机 `/data` 祖先路径下，被现有私有目录保护拒绝。原失败日志保留；第二次只改变测试运行目录，未放宽产品保护或修改测试断言。

上述前轮原件位于忽略的 `private/upload-preparation/`：候选文件摘要、构建/vet日志、两次测试日志、清理收据与独立审查分别保存。后续接入公共 CI 前，需要显式区分公开源码检查与需要受控制品/私有证据的验收；当前全量默认测试的材料要求不能被省略。

### 本轮收紧后的候选检查

用户明确临时/中间产物不随 Git 同步，原件不删除。本轮排除 181 个文件：178 个运行/过程 JSON（169 个 `*-validation.json`、4 个诊断/准备/test-definition 记录、5 个旧 docs summary）和 3 个 Spec Kit 安装回执。六份最终 acceptance、三个 approved-core 记录、正式构建 manifest/锁、candidate、fixture 与正式 decision 保持原字节；完整 SPEC、正式 Markdown 和任务状态继续保留。

root 已对收紧后的独立候选实际检查：最终候选925个文件、约9.9MB（未压缩文件总量）；固定 Go1.27.3 离线构建 `cmd/hoopa`、`go vet ./...` 均 exit0。默认测试仍 exit1：979 顶层 PASS、18 FAIL、37 SKIP，包数为25 PASS/3 FAIL/4无测试；含子事件为5581 PASS/19 FAIL/42 SKIP。19 个失败事件逐名与前轮一致，无新增失败，不能记成全库通过。原140项勾选保持一致，仍72完成。

原件仍在原位置由忽略规则外置，其 SHA 均未改；`private/upload-preparation/reduction/` 保存本轮候选、实际检查与依赖评估原件。文中指向外置报告的链接改为“本地原件”代码路径，保留原 label、路径、结果、数字和 hash；不生成重复逐轮索引或替代 PASS。

保留的正式 acceptance/批准记录仍可能引用外置报告；它们是原身份/结论承诺，不代表 clone 携带完整证据。受控 source5 gate 等复核需要在本地补齐同路径、同原始字节的运行报告和私有证据树；缺失时继续明确失败。不得为上传改核心准入、重写批准 hash 或把实际验收断言改成 skip。公共 CI 的检查与受控制品验收分层仍是后续工作。

### 临时文件与本机资料

本次仅删除公开源码目录中 11 个可再生的 Python 字节码缓存文件（147357 字节）。历史失败原件、固定核心和后续验收仍需使用的本机材料保留在忽略目录中。`.aws/`、`.codex/`、`.ssh/`、本机缓存、根目录认证文件及编辑器临时文件补充到忽略规则；15 个应忽略路径和 10 个应保留源码/SPEC 路径的检查通过。没有读取本机认证文件，也没有把其内容复制到候选中。

前轮候选文本的有限敏感模式扫描未检出可确认的真实凭据，认证字段命中为合成测试或模板占位；当时在65个记录文件发现1528行本机路径。本轮将逐轮运行/过程记录外置，并不改写原件或原摘要，也不声称保留的正式结论中所有路径已匿名化。原私有文件不上传；此前扫描不能直接当作缩减后候选的新扫描结果，更不是无敏感信息的绝对保证。

`.specify/init-options.json`、`integration.json` 和 `workflows/workflow-registry.json` 是继续工作的共享配置/索引，继续保留；不能仅因由工具生成就排除。两个 integrations 安装 manifest 和 `memory/.constitution-template.json` 安装快照外置。每个 checkout 的 `feature.json` 和 extension 的 `local-config.yml` 仍忽略；Spec Kit1.0.6版本来源文字及许可证保留，选择当前规格的方法见[开发指南](development.md#继续现有-spec)。

## 建议的提交说明

建议题目：`Prepare DB-free P1 source snapshot and validation index`

建议摘要：

- 收纳阶段实现、测试、完整 specs 工作材料、.specify 宪章/模板/脚本、共享 .agents skills、核心文本补丁及正式验收结论。
- 覆盖订阅更新、来源编译、应用规则、显式 init、run 与只读诊断等当前有限路径。
- 将 README 收敛为当前入口，保留历史失败与各阶段验收链接。
- 明确外部批准核心前置条件、72/140进度、T058真实 Agent 缺口及 P2/P3未完成边界。
- 附上候选干净构建/vet通过、默认测试因外部材料不齐而失败的实际记录，不将阶段快照描述为完整发行。

此说明不是版本号、发行承诺、自动安装/自启动方案或完整支持声明。默认不启用 TUN、系统代理或计量；源码可审查不代表任意现成核心和任意主机均可直接运行。
