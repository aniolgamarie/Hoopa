# 同源候选构建与 G01 失败记录

日期：2026-10-22。范围：用户在隔离提案与授权问题后再次调用implement，继续实施/构建隔离实验核心及受控回环。
完整项目未交付，P1未放行；机器摘要（本地原件 `native-candidate.summary.json`，不随Git上传）保存实际结果与私有原文SHA-256引用。
候选代码与身份见[manifest](../../experiments/mihomo-events/candidate-manifest.json)，可审查文本见[补丁](../../experiments/mihomo-events/patches/README.md)。

## 固定来源与构建

源码为Mihomo v1.19.26 / `fc8c5a24b16991f98cd736950c17d1aa306a5041`，在新的私有/tmp副本应用16文件改动。
补丁SHA-256为`e871aea91fdad62647d8f81ba146e799279616c3d64b53d0768b52457faed0d3`，固定checkout的apply check通过。

候选路径为`/tmp/hoopa-mihomo-events-ildaobd2/mihomo-hoopa-candidate`，版本`hoopa-p0-fc8c5a2`，Go1.27.3；
SHA-256为`76e0e114ac9a669ec0eb131f35ead972d3e24b2dceeb270e72a3e879f5b9c32e`，大小50,429,542字节。
该体积属于外部核心，不是Hoopa管理器的二进制预算测量；内存/CPU/吞吐没有本轮基线，不宣称达标。
模块使用固定上游go.mod/go.sum，无新增依赖，公共下载校验开启；未替换原78f3d2… dirty二进制或启动系统服务。

最终独立产物复核从固定Git对象另导出新副本，补丁check和实际apply均成功；16份变更摘要及917文件完整树与构建源码一致，baseline的909文件保持不变。实际二进制的99个有效依赖版本/校验和均在固定go.sum中，版本、大小和摘要与manifest一致。该复核未重新构建二进制，不构成可复现构建证明，也不提升身份或恢复门槛。详细复核报告留在`/tmp/hoopa-native-final-artifact-review.md`及同名JSON中。

## 已执行检查

| 检查 | 结果 | 证据边界 |
|---|---|---|
| 最终原生来源/钩子定向race | 23通过，0失败/跳过 | 真实执行、count=1；未跑整个上游测试集 |
| 来源写入/容量/坏帧/锁/epoch/重投 | 包含在23项中 | 保留独立终末和gap额度；源码发送端收到503后重投同ID/原文；不等于完整G02 |
| 关闭/规则/快路径 | 包含在23项中 | Bind先于Join；controller直接关闭时计数终止/final先于Leave；stock顺序保留；AND三值和unsupported纯断言 |
| vet/gofmt/构建 | 通过 | 固定Go、明确包及隔离制品；原生构建无.git故使用buildvcs=false |
| Hoopa计数codec定向race/vet | 通过 | 原5顶层/111子用例，精确大数/类型拒绝；没有把失败的身份集成混为通过 |
| 真实单流smoke | 通过传输/来源绑定子断言 | 1opened/1closed，5事件，字节/目标精确；身份owner_visibility，未要求其通过身份 |
| 真实100流身份断言 | **失败，exit1** | 两同名进程/两域名，完整100分母，见下表 |

初次smoke的受控helper在comm中多写了换行；原目录与结果保留。
在100流预登记前修正helper及名称复核，另跑单流smoke确认名称为hoopa-probe；没有修改100流失败分母或重分类样本。

## G01 实际观察

`identity_test.go`显式启动固定hash候选，两个同名helper各发50条CONNECT，目标为两个本地受控域名。
每条truth在请求解析/选路前已由父进程写入并fsync，子进程收到go确认才继续；相同source tuple与目标关联，不用时间近似。

| 项目 | 实际结果 |
|---|---:|
| 预登记 / truth / opened / closed | 100 / 100 / 100 / 100 |
| 源进程 / 域名数 | 2 / 2 |
| 精确终末累计字节 | 100/100 |
| 精确目标域名 | 100/100 |
| 精确PID+名称+启动身份 | **0/100** |
| owner_visibility | **100/100** |
| 缺口事件 / probe执行错误 | 0 / 0 |

缺口事件为0仅描述此次已完整持久化的来源事件；100个身份未知仍明确为未知，不代表完整维度或G01通过。
实际原文目录为`/tmp/hoopa-native-probe-2uzvik_w`；Go断言日志为`/tmp/hoopa-mihomo-events-ildaobd2/g01-native-identity.log`。
原文包含仅本次合成受控配置/truth/帧及来源清单，默认不提交；临时目录以后可能被清理，摘要不能替代缺失原文重验。

原始socket已在同一核心入口观测，仍无法完整验证owner集合；保守结果是unknown，而不是转发器PID、启动器PID或首个可见owner。
当前结果不能证明“普通权限在本宿主足以支持完整进程归属”。未试图改变系统权限、开启特权采集或忽略不可见进程。

## 门槛与后续

T126完成的是可运行候选与审查/身份/能力记录，不能代表完整来源能力通过。
T127保持未完成：100流身份断言失败，PID复用、共享/转交FD、后台后代等其余矩阵尚未完成。
依赖它的T128/T129未执行，T012–T018仍未关闭，P1/P2/P3不推进。

建立耗时、实际两节点链、重试流量按attempt拆分、周期checkpoint、epoch终末、配置加载期拒绝/热重载世代、
完整源故障/回收/接收journal/SQL联动和权限矩阵均未通过；不能以单路线成功或纯测试替代。
下一步需在owner集合可证明可见的合适Linux环境重跑，或针对不可见/共享源的识别前提给出具体调整。
保持原100%正确率要求，不默认提权，不将已预登记可识别样本改成unknown类以通过。
