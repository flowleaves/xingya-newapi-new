# Xingya rc37 移植 — 独立安全审查与硬性安全要求

> 范围：`xingya-newapi-new`（clean rc37 `385d2dfd` + 签到 / 自助补回空移植）
> 方法：**对抗性代码审查 + 可执行回归测试 + 生产 dump 元数据核对**。
> 结论分三级：**已修复（含测试证据）** / **未修复待决策** / **已验证安全**。
>
> 本文件是审计基线，不是上线授权。§5 的验证缺口未补齐前不得声称「数据库兼容」。

---

## 1. 本次审查做了什么

1. **继续移植**：签到分档（规则 A/B/C）+ 自助补回空（钱包 / 订阅双资金源）在 rc37 上落地。
2. **补回归测试**：新增 `model/log_refund_test.go`、`model/checkin_test.go`、`service/self_refund_test.go`，共 **35 个用例（含子用例 41 个）**。
3. **对抗性审查**：对每一条资金路径追问「攻击者能做什么」，并用**变异测试（mutation testing）**证明测试非空转。
4. **源数据库完整性专项**：站长要求「补回不得损坏源数据库」，据此审查本功能的**全部写操作**（§5）。
5. **生产数据核对**：对 `sql/newapi.sql` 做**仅元数据 / 聚合**核对（不读行内容用于展示）。

审查中发现的问题**不全是移植引入的**：F-1 / F-3 / F-4 是新移植代码的缺陷，F-2 是边界防御缺口，
F-6 ~ F-11 是设计层面的加固项（本次全部修复）。**F-9 是本轮最严重的一项**：
补回原本会 UPDATE 生产最大的 `logs` 表。

---

## 2. 已修复问题（含证据）

### F-1 · 高 · 订阅补回在单连接池上**永久死锁** — 已修复

**缺陷**：`DoSelfRefundSubscription` 在**已开启的事务内**调用 `GetDBTimestamp()`。
该 helper 查询的是包级全局 `DB`，需要**第二条连接**。在 `MaxOpenConns(1)`
（受支持的 SQLite 配置，也是 model 测试基座）下，事务持有唯一连接 →
该查询永久阻塞。

**证据**：`TestDoSelfRefundSubscriptionRestoresUsedQuotaOnly` 修复前
**挂起 > 600s 直至超时**；修复后 **0.00s** 通过。

**修复**：`model/log_refund.go` 新增 `dbTimestampTx(tx)`，通过**当前事务**读库时间。
次要收益：三种数据库上该时间值都改从**事务快照内**读取，不再读快照外。

> 说明：修复刻意放在 Xingya 自有新文件内，不改动上游共享的 `model/db_time.go`，
> 以降低后续并上游的冲突面。

### F-2 · 中 · `QuotaFromCheckinTier` 对 ±Inf **未 fail-closed** — 已修复

**缺陷**：该函数挡了 `NaN` 与 `<= 0`，**没挡 `±Inf`**。
`+Inf` 经 `common.QuotaRound` 会**饱和到 `common.MaxQuota`（int32 上限）**——
即「无穷大的配置值 → 发放平台允许的最大额度」，与「异常即不发」的兜底语义相反。

**可达性**：当日不可达。`CheckinSetting.Sanitized()` 会把 `Inf` 夹到
`MaxCheckinRewardTier`，正常链路拿不到 `Inf`。属**纵深防御**修复。

**证据**：`TestQuotaFromCheckinTierRejectsNonFinite` 修复前
`expected 0 / actual 2147483647`；修复后通过。

### F-3 · 中 · 判定为「可补回」但 POST 必然失败（口径不一致） — 已修复

**缺陷**：`MinRefundQuota` 默认 0 时，`baseQuota × ratio` 截断为 0 也能通过
`refundAmount < MinRefundQuota` 判定 → 列表**展示为可补回**，
而 `DoSelfRefundWallet/Subscription` 以「补回金额无效」拒绝。

**修复**：判定阶段即拒绝 `refundAmount <= 0`。
**证据**：`TestJudgeSelfRefundRejectsRefundThatRoundsToZero`。

### F-4 · 低 · GET 与 POST 的选择器语义**不一致** — 已修复

**缺陷**：`RefundableLogQuery.ExpectLogId` 只在 `service.DoSelfRefund` 内部赋值；
`controller.GetSelfRefundable` 从不赋值。于是
「`request_id` 与 `log_id` 指向不同日志」这一情形：
**POST 拒绝**，**GET 却按 `request_id` 静默解析**。

**修复**：删除冗余的 `ExpectLogId`（它恒等于 `LogId`），
`resolveRefundLog` 直接以 `LogId` 作一致性断言 → 两条路径共用同一规则。

**证据**：`TestResolveRefundLogEnforcesSelectorsAndOwnership`、
`TestDoSelfRefundRejectsMismatchAndCreditsOnce`。

### F-5 · 低 · 死参数 — 已修复

`service.DoSelfRefund(c *gin.Context, ...)` 内含 `_ = c`，参数从未使用。
已移除参数与随之失效的 `gin` import。

---

## 3. 资金完整性修复（F-6 ~ F-8）

### F-6 · 中 · 每日补回限额在事务**之外**判定（TOCTOU） — 已修复

**缺陷**：`DoSelfRefund` 先 `GetTodayRefundStats` 判额度，**之后**才开事务插幂等行并放款。
并发请求针对**不同**日志时，可同时通过 `DailyMaxCount` / `DailyMaxQuota` 检查。

**修复**：限额判定移入**同一个事务**，并在其之前对**用户行加锁**
（`lockUserForRefundTx` → `checkDailyRefundLimitsTx` → 幂等插入 → 放款）。
锁用户行而非当日退款行，是因为一天中第一笔退款还没有行可锁。
两条资金路径都是「先锁用户、再锁订阅」，锁序一致，不会互相死锁。

**证据（变异测试）**：把限额判定改回事务外 → `TestDoSelfRefundDailyLimitSurvivesConcurrentRequests`
在 `-count=20` 下**多次 FAIL**（上限被突破）；修好后 20 次全绿。

### F-7 · 中 · `POST /api/log/self/refund` **无速率限制** — 已修复

同级动钱路由 `POST /api/checkin` 挂了 `middleware.UserCriticalRateLimit("checkin")`，
补回 POST 只有 `middleware.UserAuth()`。

**修复**：`middleware.UserCriticalRateLimit("self_refund")`。

### F-8 · 中 · 钱包上限口径不一致（高余额用户永久无法补回） — 已修复

| 写入路径 | 修复前上限 | 修复后 |
|---|---|---|
| `creditWalletQuotaTx`（补回钱包） | `common.MaxQuota`（int32） | **`common.MaxWalletQuota`（2⁵³−1）** |
| `increaseUserQuota`（充值 / SQLite 签到） | `common.MaxWalletQuota` | 不变 |

**后果**：钱包余额一旦 **> int32 上限**（充值路径允许），补回恒判「钱包额度已达上限」。
**生产实测**：`users.quota` 最大 488,050,668（int32 上限的 22.7%），暂无人越界 → 属潜在缺陷。

**修复**：补回改用 `common.MaxWalletQuota`，与钱包自身入账路径同口径；
并复用 `common.ValidateWalletQuota` 校验单笔金额。
**证据**：`TestDoSelfRefundWalletWorksAboveInt32Balance`（修复前必然失败）。

---

## 4. 硬性安全要求（MUST）

任何后续改动本功能（补回空 / 签到）的提交**必须**满足下列可测要求。
每条都已有对应用例或明确的验证方式。

| # | 要求 | 覆盖方式 |
|---|---|---|
| **R-1** | **归属在 SQL 层强制**：任何补回目标日志 / 订阅的查询必须以 `user_id` 作 SQL 谓词，不得只靠内存判断 | `TestGetRefundableCandidatesScopesToOwner`（变异测试验证非空转）、`TestResolveRefundLogEnforcesSelectorsAndOwnership` |
| **R-2** | **幂等由数据库唯一约束保证**，不得用「先查后写」代替；失败必须回滚幂等行，使请求可重试 | `TestDoSelfRefundWalletCreditsOnce`、`TestDoSelfRefundWalletRefusesToBreachCeiling` |
| **R-3** | **金额纯服务端计算**：补回额只由服务端持有的日志字段 × 已钳制的比例得出，**绝不接受客户端传入金额**；`ratio` 必须钳制到 `[0,1]` | `TestJudgeSelfRefundClampsRatioAboveOne` |
| **R-4** | **资金源不得串流**：钱包补回只加 `users.quota`；订阅补回只回减 `amount_used`，**永不铸钱包额度** | `TestDoSelfRefundSubscriptionRestoresUsedQuotaOnly`、`TestDoSelfRefundSubscriptionClampsAmountUsedAtZero` |
| **R-5** | **异常一律 fail-closed**：`NaN` / `±Inf` / 越界 / 非正金额 → 不放款 | `TestQuotaFromCheckinTierRejectsNonFinite`、`TestJudgeSelfRefundRejectsRefundThatRoundsToZero`、`TestJudgeSelfRefundRejectsNonFiniteRatio` |
| **R-6** | **每次入账有界且校验影响行数**：上限钳制 + `RowsAffected` 校验，0 行（用户缺失/软删）必须回滚 | `TestDoSelfRefundWalletRejectsMissingUser`、`TestUserCheckinRefusesToBreachWalletCeiling` |
| **R-7** | **事务内不得读全局 DB**：事务中所有查询必须走 `tx`，否则单连接池死锁 / 读到快照外 | F-1 的修复与 `service` 侧 `MaxOpenConns(1)` 基座 |
| **R-8** | **选择器口径唯一**：GET 与 POST 共用同一解析规则；`request_id` 与 `log_id` 不一致必须拒绝 | `TestResolveRefundLogEnforcesSelectorsAndOwnership` |
| **R-9** | **鉴权 + 限流**：动钱路由必须在 `UserAuth` 之后且带用户级限流 | `router/api-router.go` + F-7 |
| **R-10** | **可审计**：每次补回必须落 `log_refunds`（幂等 + 审计）与一条 `type=6` 日志 | `TestDoSelfRefundWalletCreditsOnce` |
| **R-11** | **不得改写原请求日志**：补回对 `logs` 只能 INSERT（`type=6`），绝不 UPDATE/DELETE 既有日志行 | `TestSelfRefundNeverRewritesSourceLog`（变异测试验证） |
| **R-12** | **不得重塑既有表结构**：只在表**缺失时**创建；表已存在则不得 ALTER / 加索引 / 改列 | `TestEnsureLogRefundTableNeverReshapesAnExistingTable`（变异测试验证） |
| **R-13** | **单列写入**：订阅补回只允许改 `amount_used`，禁止整行写回（并发下会覆盖他人改动） | `TestDoSelfRefundSubscriptionChangesOnlyAmountUsed` |
| **R-14** | **跨库边界**：资金表（`log_refunds`/`users`/`user_subscriptions`）走主库；`logs` 只走日志库且只读+追加 | §3 写清单 |

### 已验证安全的项（不构成缺陷）

- **补回寻址键不可被攻击者控制**：`middleware/request-id.go` **总是**用
  `common.NewRequestId()`（时间戳 + 前缀 + 8 位随机）生成，**忽略客户端请求头**；
  `relay_info.go` 取不到时同样回落到 `NewRequestId()`。因此
  `request_id` 既不可猜测、也不可由客户端指定 → 补回日志 `Content` 中内嵌的
  `request_id` **不含攻击者可控文本**，寻址也无法被冒用。
- **跨用户订阅补回被拒**：`TestDoSelfRefundSubscriptionRejectsForeignSubscription`。
- **不合格补回被拒**：非 chat 路径、含 `tool_surcharges`、已补回、超窗口、正常结束的流式回答
  （`TestJudgeSelfRefundRejectsNonChatPathAndRefundedLogs`、
  `TestJudgeSelfRefundTruncationRequiresIncompleteAnswer`）。
- **订阅并发**：`lockForUpdate` 对 MySQL/PG 生效，串行化同一订阅的并发补回；
  SQLite 依赖单写者模型（**未做并发实测**，见 §5）。
- **签到奖励有界**：`Sanitized()` 归一化 NaN / 反转区间 / 超限值，
  `CalcCheckinTierC` 处理 `CStepQuota=0`，SQLite 路径的失败回滚已验证
  （`TestUserCheckinRejectsZeroReward`）。

---

## 5. 源数据库完整性修复（F-9 ~ F-11）

> 站长要求：**补回功能不得损坏源数据库**。以下为专项审查与修复，
> 并以「本功能对生产库的写操作清单」收口。

### F-9 · 高 · 补回会 UPDATE 生产 `logs` 表（最大表） — 已删除该写入

原实现有一次 `other.refunded=true` 的 best-effort 回写：

```sql
UPDATE logs SET other = (other::jsonb || '{"refunded":true}'::jsonb)::text WHERE id = ?   -- PG
UPDATE logs SET other = json_set(other, '$.refunded', true) WHERE id = ?                  -- MySQL/SQLite
```

两个问题：

1. **没有任何读取方**。全仓检索 `refunded` 确认：已补回状态**只**以 `log_refunds` 为准
   （`GetRefundedLogIds` + `log_id` 唯一索引），该标记既不被本服务读，也无前端/管理端消费者。
   为一个无人读的标记，让补回获得**改写请求日志**的能力，不成立。
2. **盲合并 JSON，只在 `other` 是 JSON 对象时安全**。生产 dump 实测（580,539 行）：

   | `other` 形态 | 行数 | 该表达式的行为 |
   |---|---|---|
   | JSON 对象 | 525,601 | 安全 |
   | **空串** | **54,938** | `''::jsonb` → **报错**（语句失败，当前表现为静默无效） |
   | 数组 / 标量 | 0（当前） | **静默改写**：`'[]'::jsonb \|\| '{...}'` → `[{...}]` |

   即：今天不会**损坏**数据（非对象形态当前为 0），但会在 **9.5% 的日志上报错**；
   一旦 relay 将来写入数组/标量，就会**静默破坏被补回日志的载荷**。

   > **⚠️ 自我更正（实测后）**：上面这句「会在 9.5% 的日志上报错」**是错的**，
   > 属于拿全表空串比例直接外推，没有验证它能否落到补回路径上。实测两条证据：
   > ① 生产已发生的 **1,109 次补回**，其源日志 `other` **100% 是 JSON 对象**（0 空串 / 0 数组）；
   > ② **结构上不可能不是对象**——能通过 `JudgeSelfRefund` 的日志，`other` 必须先成功
   > 反序列化成 map 才能取到 `request_path` 并通过 chat 路径校验；空串 / 数组 / 标量 /
   > `null` / 非法 JSON **全部在那里被拒**（`TestJudgeSelfRefundRequiresJsonObjectOther` 已固化）。
   >
   > 因此 F-9 的**真实**定位是**潜伏隐患**而非线上故障：危险分支当前**不可达**，
   > 但可达性依赖一个**距离该 SQL 很远的非局部不变量**。删除它的理由不是"正在出错"，
   > 而是「没有任何读取方 + 白写一张大表 + 让补回依赖对日志库的写权限 +
   > 一个改坏无关校验就会静默损坏审计数据的雷」。**不处理不会立刻出事**，
   > 但也没有任何理由留着它。

**修复**：**整体删除**该回写（`service.backfillRefundedFlag`、`model.BackfillRefundedFlag` 一并移除）。
删除后本功能对生产库的**全部**写入为：

| 目标 | 操作 | 库 |
|---|---|---|
| `log_refunds` | INSERT（幂等行） | 主库 |
| `users.quota` | 条件 UPDATE（带上界 + RowsAffected 校验） | 主库 |
| `user_subscriptions.amount_used` | 条件 UPDATE（**单列**） | 主库 |
| `logs` | **仅 INSERT**（`type=6` 补回流水） | 日志库 |

**无 DELETE、无 ALTER、无对既有行的 UPDATE。**

**证据**：`TestSelfRefundNeverRewritesSourceLog` 用三种 `other` 载荷
（JSON 对象 / 空串 / 数组）断言补回后**原日志行逐字段不变**；
变异测试（把该回写加回去）→ 测试 **FAIL**。

### F-10 · 中 · `AutoMigrate(&LogRefund{})` 可重塑生产审计表 — 已改为「仅建表」

生产 `log_refunds` **已存在**，且带**两个**历史唯一对象（仅差一个 `s`）：

```sql
CONSTRAINT idx_log_refunds_log_id UNIQUE (log_id)   -- 约束式
INDEX      idx_log_refund_log_id   UNIQUE (log_id)   -- 索引式
```

把它放进 `AutoMigrate` 列表，等于让 Go 结构体——而非经审查的迁移——有权对这张
**资金审计表**增删列/索引。`SCHEMA-COMPATIBILITY-REPORT.md` §3.1 已警告「不得新增第二个唯一约束」。

**修复**：从 `AutoMigrate` 列表移除，改为 `ensureLogRefundTable(DB)`：**表不存在才建，
已存在则完全不动**。表缺失时仍会创建 `log_id` 唯一索引（「插入即幂等」依赖它）。

**证据**：`TestEnsureLogRefundTableNeverReshapesAnExistingTable`（构造一张故意缺少该索引的既有表，
断言初始化**不会**替运营方补上索引）、`TestEnsureLogRefundTableCreatesWhenAbsent`；
变异测试（改成总是 AutoMigrate）→ **FAIL**。

### F-11 · 中 · 订阅补回整行写回，可覆盖并发改动 — 已改为单列写入

`tx.Save(&sub)` 会 UPDATE **整行**。若计费结算、周期重置或管理端在本次读取之后
改动了同一订阅的其它列（`status` / `end_time` / `last_reset_time` / `downgrade_group` …），
整行写回会把这些改动**覆盖回旧值**。

**修复**：改为 `Update("amount_used", newUsed)` 定向单列更新 + `RowsAffected` 校验。
补回从此在结构上**没有能力**改动订阅的其它列。

**证据**：`TestDoSelfRefundSubscriptionChangesOnlyAmountUsed`（整行逐字段比对）。

---

## 6. 未验证 / 阻塞项（**不得声称完成**）

| # | 缺口 | 状态 |
|---|---|---|
| **V-1** | **MySQL / PostgreSQL 未实测** | 🟡 **站长决定不再实机跑库**（原计划的 PG 18.4 + 生产 dump 复现已中止，测试集群已清理）。`AGENTS.md` 仍要求「三库矩阵、只跑一种方言不是替代品」，故此项**降级为已接受的残余风险**，不是"已验证"。已用静态方式补偿：F-9 已删除唯一的跨方言 JSON 写；`dbTimestampTx` 的 PG 分支与 `lockForUpdate` 保持与上游一致。 |
| **V-2** | **并发未实测** | 🟢 **部分缓解**：F-6 的 TOCTOU 已用并发用例 + 变异测试证实（SQLite 单连接下 20 次稳定复现「修复前 FAIL / 修复后 PASS」）。MySQL/PG 上依赖用户行锁 `FOR UPDATE`，**未实测**。 |
| **V-3** | **前端已接入** | ✅ **已完成**。独立补回页 `/self-refund`、日志详情弹窗内联补回卡、侧边栏/移动端入口、管理端「补回设置」区、7 语言 i18n、`routeTree.gen.ts` 已再生成。门禁：`typecheck` 0 错、`oxlint` 0 错 0 警、`usage-logs` 167 测试全绿、`bun run build` 成功。**未做浏览器实测**（见 §8）。 |
| **V-4** | **生产迁移未执行** | 🔴 未做。见 `SCHEMA-COMPATIBILITY-REPORT.md` §5：`log_refunds` owner 为 `newapi_user`（其余 34 表为 `user_DGrxGm`），运行角色权限**必须在部署前核对**。 |

### 已知的上游既有失败（与本移植无关）

在**未改动的 pristine rc37**（`git worktree` 独立检出）上复现，故**非本次引入**：

- `service`：`TestObserveChannelAffinityUsageCacheByRelayFormat_MixedMode` /
  `_UnsupportedModeKeepsEmpty`（且存在执行顺序依赖，单独跑只挂 1 个）。
- `controller`：`Test*DatabaseMatrix`、`TestSecurityLogin*`、`TestGenerateOAuthCode*`、
  `TestOAuthBindProviderErrorConsumesSessionBoundFlow` 等（需外部数据库/Redis，本沙箱不提供）。

**本次验证命令与结果**：

```
gofmt -l <改动文件>                       → 空（干净）
go build ./...                           → exit 0
go vet ./model/ ./service/ ./controller/ ./router/ → exit 0
go test ./model/                         → ok   9.812s  （25 个新用例）
go test ./service/ -skip TestObserve...  → ok   1.150s  （9 个新用例）
go test ./setting/... ./common/ ./router/ → ok（全部）
```

---

## 7. 对上线门禁的增量要求

在 `XINGYA-REWRITE-PLAN.md` §10 的审计门禁基础上，追加：

**已完成（本次）**

- [x] **F-6** 每日限额移入事务 + 用户行锁（并发用例 + 变异测试证实）。
- [x] **F-7** `POST /api/log/self/refund` 加用户级限流。
- [x] **F-8** 钱包补回上限对齐 `common.MaxWalletQuota`。
- [x] **F-9** 删除对 `logs` 表的 JSON 回写（R-11）。
- [x] **F-10** `log_refunds` 改为仅建表，不重塑既有表（R-12）。
- [x] **F-11** 订阅补回改单列写入（R-13）。

**仍需在部署前完成（未满足）**

- [x] **V-3** 前端接入 Phase D（补回页 / 日志详情卡 / i18n / 路由再生成）。
- [ ] **V-5** 浏览器实测：补回页与内联卡的点击、loading、错误态与移动端布局（本沙箱无法跑浏览器）。
- [ ] **V-4** 核对 `log_refunds` 运行角色 `SELECT/INSERT/UPDATE` + 序列 `USAGE`。
- [ ] **V-1** 若后续恢复三库验证：优先补 PG（生产 18.4）上的补回 + 签到回归。
- [ ] **V-2** 若后续恢复：MySQL/PG 上验证用户行锁对并发补回的串行化。

---

## 8. 本次审计的局限（诚实声明）

- 审查由**同一执行者**完成移植后立即进行，虽采用对抗性提问 + 变异测试降低确认偏差，
  仍**不能替代第三方独立复核**。
- **SQLite 是唯一被实际执行的数据库**。V-1 是最大的未覆盖风险，且**已由站长决定接受**
  （不再实机跑库），因此**不得据此声明"数据库兼容"**。
- 未做模糊测试、未做 HTTP 层渗透测试、未验证前端渲染路径
  （`refundLogContent` 的展示端是否转义 HTML **未核实**——虽已确认
  `request_id` 非客户端可控，仍建议在前端侧复验）。
- 对生产 dump 只做了**表/列/索引元数据与数值聚合**（以及 `logs.other` 的**首字符形态分类**），
  未读取或导出任何行内容。
- 曾短暂准备 PostgreSQL 18.4（与生产同版本）以复现生产 dump，**按站长指示中止并已清理**
  （`.pg18*` / `.pg18data` 已删除，无残留进程）。
- 生产 `logs.other` 形态分类的复算脚本保留在 `.gotmp2/check_other_shape.py`。
- **V-3 前端未做浏览器实测**：`typecheck` / `oxlint` / 167 个组件测试 / 生产构建均通过，
  但没有真实点击过补回按钮、未验证 loading / 错误态 / 移动端断点表现。见 **V-5**。
- `bun run format:check`（oxfmt）**无法在本沙箱执行**：oxfmt 需要 spawn 外部格式化器，
  被沙箱的命名管道限制拦成 `spawn EPERM`。受影响文件按既有风格手工排版，
  且 `oxlint` 对该批文件 0 错 0 警；**格式化检查需在正常环境补跑**。
