# 上游参考与证据边界

定向核对日期：2026-10-21。本次阅读官方文档与相关源码，不代表已重新审查所有上游最新 release。未取得固定 commit 的分支链接是可变资料；P0 必须另外固定二进制、源码版本和摘要，才能形成可重复的运行结论。

## Mihomo

| 一手资料 | 本次能确认的内容 | 对 Hoopa 的设计影响 |
| --- | --- | --- |
| [Proxy providers](https://wiki.metacubex.one/en/config/proxy-providers/) | HTTP/file provider、更新周期、path 与 provider health check；解析备用 payload 有说明 | Hoopa 自己维护可用快照和更新调度；不能把解析备用等同于上一份快照保证 |
| [代理组通用配置](https://wiki.metacubex.one/config/proxy-groups/) | `use`、`proxies`、默认选择与 `empty-fallback`；无效默认项可退到首节点，空组默认回退为 COMPATIBLE；组级探测不覆盖 use 引入的节点 | 显式拒绝回退、必需组校验、选择核验；provider 节点需要其自身探测配置 |
| [Controller API](https://wiki.metacubex.one/en/api/) | 配置重载、provider 更新、单组选择、连接和流量查询是不同端点 | 端点存在不证明跨资源事务；只读诊断排除有副作用的 GET 探测 |
| [连接 metadata](https://github.com/MetaCubeX/mihomo/blob/Meta/constant/metadata.go)与[进程查询调用](https://github.com/MetaCubeX/mihomo/blob/Meta/tunnel/tunnel.go) | 所读结构含 host、process、processPath、uid，没有可直接用于本需求的源 PID 字段；进程信息依赖源地址/端口查询，关闭或失败时可缺失 | 必须独立验证原始源进程身份；经 GOST 后的 socket 归属不能直接当作业务源进程 |
| [代理端口](https://wiki.metacubex.one/en/config/inbound/port/)与[入站安全说明](https://wiki.metacubex.one/en/config/inbound/) | SOCKS 与 mixed 入站；对外暴露未保护的代理存在风险 | 基线绑定 loopback；先关闭数据入站、再核验和开放的可行性需实测 |
| [Mihomo LICENSE](https://github.com/MetaCubeX/mihomo/blob/Meta/LICENSE) | 此路径展示 GPL v3 许可文本 | 记录被消费版本及许可；独立进程边界不替代发行时的依赖许可核对 |

这里的设计推论是：不能让供应商的任意命名、远端规则或默认回退决定 Hoopa 的本地逻辑。文档未提供 Hoopa 所需的完整热切换事务保证，因此采用可恢复的整份快照和显式中断基线；这不是对 Mihomo 内部实现原子性的完整审计。

`default-selected`、`empty-fallback`、端口动态启用等能力都要在选定核心版本上做能力检查。文档中的新字段不自动成为所有旧版本都支持的接口。

本轮对进程/域名统计作了定向补核：Mihomo API 将 connections 描述为活动连接并按周期刷新，未承诺终末事件持久交付、重放或短连接无遗漏。所读 metadata 从目标地址区分 host 与 destinationIP，嗅探域名另有字段，不能认定每条连接都有域名。由数据路径可推论 GOST 转发后核心识别的对端可能是 GOST；这不是原始 PID 透传保证。上述新增源码为可变分支，尚未固定 release/commit，也未运行实测。[统计设计](traffic-statistics.md)据此要求额外验证完整连接事实与原始归属来源。

2026-10-22 补核延迟与路由的官方文档，仍不构成固定版本验收：

- [延迟与连接 API](https://wiki.metacubex.one/en/api/)支持带目标/超时的节点探测，连接项含 chains/providerChains，组详情的 now 是当前选择。文档未提供逐连接 DNS/TCP/TLS/TTFB 时间，也未定义所有 delay 的精确计时起止；组级 delay 还会清除自动组固定选择，不能作为只读查询或纯测量捷径。
- [Unified Delay 与进程查找模式](https://wiki.metacubex.one/en/config/general/)说明探测握手差异的处理及 find-process-mode。进程识别关闭/失败的安全回退未在该页保证；Hoopa 需要独立验证三态拒绝行为。
- [路由规则](https://wiki.metacubex.one/en/config/rules/)列出 PROCESS-NAME/PATH、UID、IN-NAME/IN-PORT 等构件及从上到下匹配。PID/原进程透传和本项目跨入站顺序仍需验证。
- [Listeners](https://wiki.metacubex.one/en/config/inbound/listeners/)允许命名入站、rule/sub-rule 与直接 proxy/group 指定。直接 proxy 字段可能改变常规规则执行路径，因此本项目优先验证保留统一规则顺序的命名入站编译，不能只因字段存在就宣称应用路由已可用。

本项目的建立/首响应计时定义、窗口聚合、历史节点关联和路由失败策略是设计要求，不能归为上游已有保证；证据与实现边界分别见[流量质量设计](traffic-statistics.md)和[路由设计](routing.md)。

## GOST

| 一手资料 | 本次能确认的内容 | 对 Hoopa 的设计影响 |
| --- | --- | --- |
| [Prometheus metrics](https://gost.run/en/tutorials/metrics/) | input/output 字节 counter、metrics 配置；全局 metrics 服务与普通 service 的区别 | 选择入口指标，不将其称为供应商用量；exporter 可达不证明所有业务计数连续 |
| [metrics 定义](https://github.com/go-gost/x/blob/master/metrics/metrics.go)与[Prometheus 实现](https://github.com/go-gost/x/blob/master/metrics/prom.go) | 所读 master 源码以 host/service/client 建立 counter 标签 | 每个 series 先做差再汇总；client 不是应用或进程身份；不能将聚合结果重复加入分项 |
| [TCP 连接计数包装器](https://github.com/go-gost/x/blob/master/metrics/wrapper/conn.go)与[listener 包装](https://github.com/go-gost/x/blob/master/metrics/wrapper/listener.go) | 读写返回时依据实际 n 累加计数，而非仅在连接关闭时记账 | 长连接采样应可见，但选定 release / handler / 优化路径仍要逐项验收 |
| [Quick start](https://gost.run/en/getting-started/quick-start/)与[文件配置](https://gost.run/en/reference/configuration/file/) | service、chain、node、connector 与 dialer 的配置结构 | 形成 HTTP/TCP → SOCKS5/TCP 的最小原型模板，模板未运行 |
| [Service 文档源码](https://github.com/go-gost/docs/blob/master/en/docs/concepts/service.md)、[service 实现](https://github.com/go-gost/x/blob/master/service/service.go)、[API stats](https://github.com/go-gost/x/blob/master/api/config.go) | stats/observer 是与 Prometheus metrics 不同的输出路径 | 首期固定一种来源；另一种不作为未经验证的自动降级路径 |
| [GOST LICENSE](https://github.com/go-gost/gost/blob/master/LICENSE) | 此路径展示 MIT 许可文本 | 被安装核心及其依赖仍各自记录版本与许可 |

所读 wrapper 使用浮点 counter 接口；当计数足够大时逐字节精度必须另外验证并标质量。所读 metrics 源码没有独立的无 client 服务总量 counter；这不保证未来或其他版本始终相同。未在限定源码范围找到关闭连接即清理 counter series 的路径，因此不能把“指标仍在”解释为“连接仍活跃”。reload 后同名服务的 counter 生命周期仍待实测。

将 checkpoint、差量与 gap 同事务提交，是 Hoopa 为持久记账制定的设计，并非 GOST 已替 Hoopa 提供的保证。管理器退出后的转发、进程接管、重启归零和不可恢复尾段，都需要组合原型。

## Clash Verge Rev

| 一手资料 | 本次能确认的内容 | 可借鉴范围 |
| --- | --- | --- |
| [配置增强说明](https://www.clashverge.dev/guide/extend.html) | 用户提供的配置组合参考入口 | 本地覆盖与生成配置的组织思路，不导入完整桌面配置系统或脚本执行层 |
| [自定义规则](https://www.clashverge.dev/guide/rules.html) | 本地规则在订阅更新后保留；规则顺序与策略目标有说明 | 本地策略与远端节点分离，并向用户解释匹配来源 |
| [快速入门](https://www.clashverge.dev/guide/quickstart.html)与[日志导出](https://www.clashverge.dev/guide/log.html) | 导入、选节点、启用的基本流程；管理界面与核心日志分别存放 | 缩短首次使用路径，分类展示故障证据并提供脱敏导出 |
| [配置应用管理](https://github.com/clash-verge-rev/clash-verge-rev/blob/dev/src-tauri/src/core/manager/config.rs) | 配置生成、校验、落盘、重载，以及失败后尝试重启的管理流程 | 独立配置编译与核心控制；不能据此证明多资源原子提交 |
| [profiles 与选择恢复](https://github.com/clash-verge-rev/clash-verge-rev/blob/dev/src-tauri/src/config/profiles.rs) | 组选择持久化、逐组恢复、provider 尚未就绪时保留选择 | 区分持久意图与实际生效；不能照搬后台逐组恢复作为 Hoopa 的严格切换保证 |
| [连接数据 hook](https://github.com/clash-verge-rev/clash-verge-rev/blob/dev/src/hooks/use-connection-data.ts) | 从连接快照维护活动连接及有限历史，计算实时增量 | 轻量面板的展示参考，不用有限连接缓存替代可靠累计账本 |
| [服务核心启动](https://github.com/clash-verge-rev/clash-verge-service/blob/main/src/service/core.rs)与[sidecar 配置](https://github.com/clash-verge-rev/clash-verge-rev/blob/dev/src-tauri/tauri.conf.json) | 独立核心启动、配置测试与 sidecar 集成 | 参考管理面与数据面分离，不引入 Tauri 或默认系统服务 |
| [Clash Verge Rev LICENSE](https://github.com/clash-verge-rev/clash-verge-rev/blob/dev/LICENSE) | 此路径展示 GPL v3 许可文本 | 学习行为与架构；不把上游实现直接复制进现有 MIT 项目 |

这些源码链接来自 `dev/main` 可变分支，不是固定 release 审计。Hoopa 的 Linux CLI 使用场景也不同于桌面应用：不能将桌面退出、后台服务和 UI 缓存语义直接视为本项目已满足的保证。

[功能规划](features.md)将上述产品组织思路转化为 Hoopa 的八个模块及四项使用流程。切换影响预览、应用接入预览和静态/实际路由解释是 Hoopa 的规划，不表示 Clash Verge Rev 提供了相同接口或保证；该补充沿用本轮对话中的定向资料核对，不构成最新 release 的重新审计。

## CLI、TUI 与 CI

- [Cobra](https://github.com/spf13/cobra) 作为 CLI 层，[Bubble Tea](https://github.com/charmbracelet/bubbletea) 与 [Bubbles](https://github.com/charmbracelet/bubbles) 作为后续按需交互层。版本在开始实现时固定，核心业务不绑定这些库。
- [GitHub Actions 计费说明](https://docs.github.com/en/billing/concepts/product-billing/github-actions)区分公开仓库的标准托管 runner 与私有仓库免费额度。平台 CI 不使用大规格付费 runner；私库耗尽额度不能默认转付费，存储与制品保留也单独控制。

## 原型结果如何补充资料

每项实验在结果中同时列出文档 URL、固定源码版本、二进制摘要、实际命令、原始观察与结论。若运行行为与可变文档不符，以固定版本的复现证据提出决策，不在文档中悄悄抹去冲突。成功、失败和环境不足分开保存；性能目标只有实测后才允许写为结果。

## 2026-10-22 实测后事件源补核

[ADR 005](adr/005-telemetry-replan.md)依据固定Mihomo v1.19.26的metadata、tracker和tunnel流程重规划；API字段存在不代表已具备完整PID/启动身份、持久终末或setup事件。原可变分支资料保留作历史背景，不以最新文档冒充本地资产能力。

本地GOST报告3.3.1，Go build info主模块revision为ece7770ad78f86744aa95c7887ead065642f96b4，x v0.17.2、core v0.6.1；未验证官方发布资产来源。scout按该依赖版本核对[admission wrapper](https://github.com/go-gost/x/blob/v0.17.2/admission/wrapper/listener.go)、[HTTP admission](https://github.com/go-gost/x/blob/v0.17.2/admission/plugin/http.go)、[recorder定义](https://github.com/go-gost/x/blob/v0.17.2/recorder/recorder.go)及[file backend](https://github.com/go-gost/x/blob/v0.17.2/recorder/file.go)。同步admission有来源tuple但无PID/本地entry，file backend不提供本项目要求的Sync/序号/重放保证。

[内核proc TCP文档](https://docs.kernel.org/networking/proc_net_tcp.html)说明tuple、UID及inode字段，也注明proc接口已建议以tcp_diag替代。原入口探针仅验证受限Linux读取方法，不把可读tuple等同唯一PID证据；宿主FD权限不足时明确未验证，不默认提权。真实journal修复结果见[重规划验收](validation/telemetry-replan.md)。
