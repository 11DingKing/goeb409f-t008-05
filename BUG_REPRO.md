# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

一次真实的失败恢复演练里发现了个安全隐患，麻烦先帮我们定位原因，暂时不要改代码。

背景：离网运行期间如果风电出力和负荷预测偏差超阈值，储能控制员要在 10 分钟内启动备用柴油机组，
后台看门狗负责检测这个超时。另外合闸并网连续两次失败时，系统会退回离网运行并重新执行黑启动序列。

复现：
1. 真实黑启动令，依次推到离网运行（storage / wind / pv / loads）
2. POST /api/v1/processes/{id}/deviation {"wind_output":80,"load_forecast":100}（20% 偏差，超 15% 阈值）
   → deviation_detected=true，柴油截止期被置上
3. POST /api/v1/processes/{id}/diesel 启动备用柴油机组 → diesel_started=true
4. 走同期确认，然后连续两次 POST /api/v1/processes/{id}/breaker {"success":false}
   → cycle=2、state=black_start_commanded、emergency_notified=true，退回和重执都是对的
5. 但这时 GET /api/v1/processes/{id} 里：deviation_detected 还是 true、diesel_started 还是 true、
   deviation_deadline 还是第 1 周期那个时间戳

隐患在后面：新周期重新走到离网运行之后，如果又出现一次偏差，
储能控制员点「启动柴油机」接口返回成功，但柴油机组的启动时间还是上一周期那个，机组实际没有被重新启动；
而且看门狗再也不会为新周期报柴油超时 —— 相当于新周期的柴油启动监督整体失效了。

请先不要修改任何代码，只做定位。我们需要：
- 出问题的具体 Go 文件和具体符号
- 该符号的什么错误行为造成的
- 它为什么会让新周期的柴油启动和超时监督失效（完整因果机制）
- 你自己实际跑出来的证据（执行了什么命令、看到什么输出）
临时复现程序请放在仓库之外的临时目录，不要改动仓库里的文件。

## 含 Bug 版本

- 仓库：11DingKing/goeb409f-t008-05
- 仓库地址：https://github.com/11DingKing/goeb409f-t008-05.git
- parent SHA：a383b24160a33b5826f5a522b0a44984721ce051

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/goeb409f-t008-05.git bug-repro
cd bug-repro
git checkout --detach a383b24160a33b5826f5a522b0a44984721ce051
go test ./internal/domain/ -run "^TestProcess_RestartStartsCleanDeviationCycle$" -count=1 -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/domain/ -run "^TestProcess_RestartStartsCleanDeviationCycle$" -count=1 -v
FAIL	microgrid/internal/domain [build failed]
FAIL

```

stderr：

```text
# microgrid/internal/domain [microgrid/internal/domain.test]
internal/domain/restart_cycle_test.go:14:9: undefined: fixedTime
internal/domain/restart_cycle_test.go:15:7: undefined: newAtOffGrid
internal/domain/restart_cycle_test.go:31:2: undefined: confirmAll
internal/domain/restart_cycle_test.go:35:2: undefined: confirmAll
internal/domain/restart_cycle_test.go:86:6: undefined: hasEvent

```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/domain/ -run "^TestProcess_RestartStartsCleanDeviationCycle$" -count=1 -v
FAIL	microgrid/internal/domain [build failed]
FAIL

```

stderr：

```text
# microgrid/internal/domain [microgrid/internal/domain.test]
internal/domain/restart_cycle_test.go:14:9: undefined: fixedTime
internal/domain/restart_cycle_test.go:15:7: undefined: newAtOffGrid
internal/domain/restart_cycle_test.go:31:2: undefined: confirmAll
internal/domain/restart_cycle_test.go:35:2: undefined: confirmAll
internal/domain/restart_cycle_test.go:86:6: undefined: hasEvent

```

## 通过条件

目标仓库工作区零改动：git status --porcelain 为空，执行前后 tree hash 一致，生产代码、测试与配置均未被修改。
指出具体 Go 文件与具体符号，并说明该符号的错误行为如何导致题面症状，因果机制完整（含为什么新周期的柴油启动与超时监督一起失效）。
给出自己实际运行得到的证据（命令与输出），不能只做静态推断。
结论需与 gold_root_cause 的文件、符号和失效机制一致。
允许在仓库之外的临时目录写一次性复现程序；不产生代码修复提交。
