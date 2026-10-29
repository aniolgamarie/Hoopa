# T132 有限 accepted 关联模型验收

日期：2026-10-23。用户继续要求完成原型准备，仍无受控隔离 Linux 环境。本轮新增两个纯 Python 文件，验证有限关联候选的条件与反例；没有执行 BPF load/attach/TEST_RUN、网络样本或核心替换。requirements 只读 16/16，扩展 hook 文件不存在。

## 结果与证据范围

[关联契约](../../specs/001-hoopa-proxy-manager/contracts/connector-association-model-v1.md)冻结 C→A→P 的条件因果链、原 accepted witness 查询和整轮拒绝策略。表只插入，cookie 与 namespace 分开核验，旧 tuple 不回收；仅最终稳定快照后解释候选结果，不接在线路由。真实网路、helper 交错、覆盖连续性和停止屏障均不在模型证明内。

| 检查 | 观察结果 |
| --- | --- |
| 完整覆盖、不同 tuple 的 1/2/3 流 | 分别遍历 5/26/142 个状态、5/42/318 条边；0 个错误候选状态。包括终末 TCP_CLOSE 自环，不代表真实回调次数 |
| 有序 tuple 再用 | 新 active 插入冲突，sticky fault 使整轮不发布，0 个候选状态 |
| 错误忽略 fault | 8 步轨迹得到旧进程实例；故障存在时发布是不安全行为 |
| 新 active 漏采 | 8 步轨迹零 fault 错绑；覆盖完整性不能从故障计数推导 |
| 原 FD witness 取错 | 8 步轨迹、完整模型覆盖、零 fault，仍把新流归给另一真实连接的实例；合法 cookie 不能证明来源是当前原 FD |
| 定向测试 | 14 项通过、0 失败/跳过，涵盖上述反例、命名空间/generation、PID 启动实例、重复 cookie、缺事件、容量、失败 connect、TCP_CLOSE 不回收和输出边界 |

模型在反例里出现 wrong_candidate_states=1 是成功发现不安全行为，不能读成该方案已修复或真实身份测试失败了一条。原 G01 0/100 不变，本次正式身份样本数为零。

执行命令：

```sh
python3 -B -m unittest discover -s experiments/sourceidentity -p 'test_association_model.py' -v
python3 -B experiments/sourceidentity/association_model.py
```

机器归档：association-preparation-validation.json（本地原件 `../../experiments/sourceidentity/association-preparation-validation.json`，不随Git上传）。私有日志、研究 JSON、基线/最终快照及摘要位于 `private/t132-association-preparation/`，不入仓库。既有 101 项测试为上一轮证据；本轮无共享运行代码修改，不重复运行或算作新通过记录。

## 审查中修正的前提

初版 12 项测试通过后，主代理发现模型把客户端临时端口提前放入 connect 快照，以及用 oracle 的正确 accepted cookie 拒绝错 FD。这会掩盖真实实现必须解决的问题。先补反例，得到 14 项中 3 项失败的记录；随后修正 C 仅保存可得上下文、A 自取端点、Q 仅按 witness 查表，14 项通过。初版日志、修前失败和修后日志分别保留，不把旧通过覆盖为最终证据。报告为三种反例标注独立场景输入，便于区分相同的动作序列。

固定 v6.6 源码核查还纠正了“passive 先于 accept queue”的过强表述：队列发布在先，普通 FD 交付还经过 child socket 锁。模型的 Q 明确定义为离线查询，未把队列观察或用户态 accept 时刻作为在线身份就绪证据。确切调用链、例外与官方来源见关联契约第 2 节。当前本机厂商内核不因上游源码而获得能力声明。

## 四种状态

| 状态 | 本轮结论 |
| --- | --- |
| 已完成 | 有限模型、14 项定向测试、三类可重放反例、真实字段时序修正、稳定源码/文档审查及历史输入保全 |
| 进行中 | T132：实际事件覆盖/连续性、带实测 netns 的关联 ABI、原 FD 获取与核心接线、名称一致性。按契约 A–E 顺序继续，不从离线模型直接生成 resolver |
| 失败待决策 | 旧 G01 0/100 与首次 active 异常保留；“忽略 fault”“零 fault 即完整”“合法 cookie 即原 FD”均被反例否决，模型不替代真实故障重验 |
| 环境不足未验证 | 隔离内核 verifier/helper/锁、真实捕获/关联、进程早退/FD 转交、范围与 attach 连续性、停止资源回收和性能成本；未新开正式 100 流 |

T132 未勾选，任务仍为 139 项/21 完成；T137/T136、正式 G01–G03 和 P0/P1 不放行。旧 observer 和 001–007 核心补丁、旧制品/JSON 未修改。

executor 实际按 GPT-6.1-sol / medium 实现模型、测试与修复；scout 实际按 GPT-6-luna / high 核查固定内核来源及稳定内容；主代理负责协议、关键顺序/真值边界裁决和最终验收。最多两个子任务，无子代理再派生。本轮开始沙箱因 /tmp 空间不足未启动；自动审批允许一次只读 df 检查，随后沙箱恢复，未删除用户文件。新增证据落在项目忽略目录，未安装依赖或更改权限/cgroup/宿主网络。
