# 建连内可执行文件快照的有限验证

2026-10-24，固定 Linux 6.18.54.1/x86_64 的真实 VM 已验证 `bpf_get_task_exe_file → bpf_path_d_path → bpf_put_file` 可加载和运行，并能以原 socket cookie 向后续 connect4 准入提供快照。它为应用信息增加一个有明确来源的字段，尚未替代 kernel comm 或接入 Mihomo。见[契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-snapshot-spike-v1.md)与机器记录（本地原件 `../../experiments/sourceidentity/executable-snapshot-validation.json`，不随Git上传）。

108 项准备检查后只运行了一次 VM `vm-executable-spike-of7mbsct`。两个程序实际类型为 LSM/CGROUP_SOCK_ADDR，ID6/7、JIT773/455B；三个 map、两个 link 与实际内核 BTF 目标均核对。配置初始化后 freeze，records/state 的自有 FD 为只读。四个新 socket cookie 为 1/2/3/4：

| 阶段 | 快照 | connect 结果 | captures / connects / allowed / refused / fault |
| --- | --- | --- | --- |
| 只有准入 gate | 无 | EPERM | 0 / 1 / 0 / 1 / 0 |
| 挂接快照钩子 | `/bin/python3`，cookie2 | ECONNREFUSED | 1 / 2 / 1 / 1 / 0 |
| 自有 comm 改为 hoopa-exe-new | `/bin/python3`，cookie3 | ECONNREFUSED | 2 / 3 / 2 / 1 / 0 |
| 解除快照钩子 | 无 | EPERM | 2 / 4 / 2 / 2 / 0 |

ECONNREFUSED 来自无 listener 的测试端点，表示准入已放行且后续 TCP 连接被拒绝；这里没有代理转发流。两个成功路径记录返回长度 13（包含 NUL），与独立 `/proc/self/exe` readlink 原始字节相同；PID/TID=1、cookie 和原 connect 的 BOOTTIME 区间一致。四阶段中只有 cookie2/3 有记录，后续读取没有覆盖它们。原名称改动不影响该可执行文件观察，并不证明 comm 的原子快照能力。

实际 guest 阶段和清理均无错误，但第一版主机检查器因两处 ABI 解释错误失败，原检查器、准备报告、镜像和失败记录均保留：

- BPF_F_RDONLY 控制打开 FD 的访问方式，内核会从 map_info 的对象 flags 中移除该位。修订版分别核对对象属性和原始 fdinfo 的严格八进制 O_ACCMODE，没有把缺失或可写 FD 当成只读。
- bpf_link_info 的 union 依 8 字节对齐；tracing 的 attach_type/target_obj_id/target_btf_id 位于 16/20/24 字节，而非原检查器的未对齐偏移。修订版依据实际 BTF 生成声明，逐字段与 fdinfo 互证。

修订检查器在独立目录完成 86 项相关离线检查，然后重新检查同一份原材料，全部通过；没有重跑 VM 或新增样本。三份 guest 导出文件摘要重验，专用 virtio 证据通道完整，内核日志未发现本检查器关注的事故。LSM link 在最后一次连接前实际解除；最终 gate link、map/program FD 和 cgroup FD 关闭，原 comm 恢复。文件引用释放由源码路径和 verifier 的获取/释放规则支持；FD 关闭和 BTF free 调用不被描述为全部内核内存已经回收。

固定源码给出的边界仍须保留：普通 connect 和 io_uring connect 经 socket_connect LSM，而 MSG_FASTOPEN 可绕过它，后续缺快照必须拒绝；本轮只实际测试普通 connect。exe_file 引用保持文件对象有效，d_path 渲染当前路径视图，路径仍可能改名、删除、超长或相对不同根目录解释；不能只凭字符串认证不变的可执行文件。下一步按[路径边界契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-path-boundaries-v1.md)验证这些具体行为，再确定适用的产品字段。

[路径边界实测](connector-executable-path-boundaries.md)随后完成：改名改变新快照、超长拒绝、删除后保留原文，旧记录保持。每次均为新 socket；同 cookie 的重试与证据消费仍待单独验证。

当前正式 identity/name/path 能力、T132/T137 及 P0/P1 均未升级；139 项完成 21 项，正式 G01 新样本 0、旧 0/100 保留。
