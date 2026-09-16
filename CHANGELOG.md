# CHANGELOG — 星芽 NewAPI

## v2.0.0-rc37（重建版）

> **这不是 `xingya-newapi`（rc.25+1 快照）的升级版，而是一次换基线的重建版。**
> 基线为官方 **`v1.0.0-rc.37`（`385d2dfd`）**，比旧分叉领先 **110 个上游提交 / 12 个 RC**。
> 「订阅码」机制与旧分叉的若干自研功能**未被移植**，具体见下。

### 新增

| 能力 | 说明 |
|---|---|
| **控制台签到（分档 A/B/C）** | 规则 A=昨日计费请求数、B=昨日消耗额度、C=历史累计消耗分档，三规则取更高；未达标有兜底。含「隐形拒绝」式边界（零奖励不占用当日机会）、`Sanitized()` 归一化运营手填的 NaN/反转/越界档位、单次发放硬上限 `MaxCheckinRewardTier` |
| **自助补回空** | 空返回 / 流式截断，48h 窗口内按比例补回；**钱包与订阅双资金源**（订阅只回减 `amount_used`，**永不铸钱包额度**）；独立页 `/self-refund` + 日志详情内联卡 |
| **充值1 `/recharge`** | 内嵌官方发卡平台（iframe），侧边栏与移动端均有入口。**不含充值2** |
| **星芽品牌** | logo / favicon 三件套 / 首页（Hero+免责声明+交流群+充值方式+调用指南）/ 关于页（能力介绍+6 步教程+cURL/Python/JS/FAQ）/ `DEFAULT_SYSTEM_NAME='星芽'` / `index.html` 标题与描述 |
| **CI** | `.github/workflows/build-xingya-new.yml`：push 或手动触发 → 构建 → 推 `ghcr.io/flowleaves/xingya-newapi-new:{latest,v<日期>-<sha>}` |

### 相对旧分叉 `xingya-newapi` 的功能差异（**请确认这是预期**）

| 能力 | 旧分叉 | 本版 | 说明 |
|---|---|---|---|
| 上下文长度限制 | ✅ | ❌ | **未移植**（旧分叉独有） |
| 订阅兑换码 | ✅ | ❌ | 站长明确不要；且旧分叉的 `redemptions.plan_*/batch_*` 扩展列从未移植 |
| 按次阶梯定价 v2 | ✅→已回退 | ❌ | 旧分叉已按 ADR-023 整体回退 |
| 使用指南 `/guide` | ✅ | ❌ | 未移植 |
| 充值2 `/recharge2` | ✅ | ❌ | 站长明确只保留充值1 |
| 订阅套餐 / 支付 / 管理 | ✅ | ✅ | **保留**（rc37 原版，未改动） |
| 签到 / 自助补回空 / 充值1 / 品牌 | ✅ | ✅ | 保留并增强 |

> 若上表任一 ❌ 不是预期结果，请在合并前提出——它们是**范围决策**，不是遗漏。

### 安全修复（本版已完成，旧分叉未同步）

| 编号 | 问题 |
|---|---|
| F-1 | 订阅补回在事务内读全局 DB → **单连接池永久死锁**；改为事务内读 `dbTimestampTx(tx)` |
| F-2 | `QuotaFromCheckinTier` 对 `±Inf` 未 fail-closed（会饱和成平台最大额度）；已拒绝非有限值 |
| F-3 | 补回金额截断为 0 时仍判为「可补回」而 POST 必失败；判定阶段即拒绝 |
| F-4 | GET 与 POST 选择器语义不一致；统一为「`request_id` 与 `log_id` 同时给出必须指向同一条」 |
| F-5 | `service.DoSelfRefund` 死参数 |
| F-6 | 每日补回限额在事务**外**判定（TOCTOU）；改为事务内 + 用户行锁（含并发回归测试） |
| F-7 | `POST /api/log/self/refund` 无速率限制；补 `UserCriticalRateLimit("self_refund")` |
| F-8 | 补回钱包上限用 int32 `MaxQuota`，与钱包域 `MaxWalletQuota` 不一致 → 高余额用户永久无法补回；已对齐 |
| F-9 | 补回会 **UPDATE 生产 `logs` 表**（盲合并 JSON，空串报错、数组会被静默改写）；**整体删除**该回写。删除后本功能对库的写入仅剩：`log_refunds` INSERT、`users.quota` 条件 UPDATE、`user_subscriptions.amount_used` 单列 UPDATE、`logs` 仅 INSERT |
| F-10 | `AutoMigrate(&LogRefund{})` 可重塑生产审计表；改为 `ensureLogRefundTable()` **仅建表**，已存在则完全不动 |
| F-11 | 订阅补回整行 `Save` 会覆盖并发改动；改为只更新 `amount_used` |

**硬性安全要求**（R-1~R-14）与完整审查报告见 `docs/xingya/SECURITY-REQUIREMENTS.md`。

### ⚠️ 上线前必须完成（未验证项）

| # | 项 | 状态 |
|---|---|---|
| **V-1** | **MySQL / PostgreSQL 未实测**（只有 SQLite） | 据 `AGENTS.md`，不得据此声明「数据库兼容」 |
| **V-4** | `log_refunds` 运行角色权限未核 | owner 为 `newapi_user`，其余 34 表为 `user_DGrxGm`；检查语句见 `docs/xingya/v4-permission-check.sql` |
| **V-5** | 浏览器未实测 | typecheck / oxlint / 组件测试 / 生产构建均通过，但未真实点击 |

### 数据库迁移注意（唯一会 DROP 生产对象的操作）

rc37 会把 `prefill_groups.name` 的**全局唯一约束** `idx_prefill_groups_name` 删除，
换成部分唯一索引 `uk_prefill_name ... WHERE deleted_at IS NULL`（带表锁 + 事务 + 幂等）。
该表生产 **0 行**，但仍须：**备份 → sanitized 副本演练 → 验证回滚（旧镜像重启是否会重建双索引）**。

Schema 兼容性结论见 `docs/xingya/SCHEMA-COMPATIBILITY-REPORT.md`。
