-- V-4 检查：log_refunds 的属主 / 权限 / 序列 / 索引
--
-- 用途：确认 NewAPI 的「运行角色」真的能读写 log_refunds（自助补回靠它做幂等
-- 与审计）。权限缺失时退款会在插入时失败，并被上层吞成「该请求已补回」，
-- 用户看不到真实原因。
--
-- 用法（整段文件用 psql -f 执行，不要粘进 bash）：
--
--   # ① 读属主/索引（任意能读 catalog 的角色即可，通常是超管）
--   docker exec -i 1Panel-postgresql-J4oR \
--     psql -X -q -v ON_ERROR_STOP=1 -U postgres -d newapi -f - < v4-permission-check.sql
--
--   # ② 决定性验证（必须用「运行角色」，否则结论无意义）
--   docker exec -i 1Panel-postgresql-J4oR \
--     psql -X -q -v ON_ERROR_STOP=1 -U user_DGrxGm -d newapi -f - < v4-permission-check.sql
--
-- ★ 运行角色从 NewAPI 容器的 SQL_DSN 里取，例如：
--     docker exec <newapi容器> printenv SQL_DSN
--   SQL_DSN=postgres://<user>:<pw>@<host>:5432/newapi  →  <user> 就是运行角色
--
-- 全部语句只读，唯一会写的是第 C 段（写入后立即 ROLLBACK）。

\pset pager off
\echo ''
\echo '================ A. 属主与对象（只读） ================'

\echo '--- A1. 关键对象属主（预期：仅 log_refunds 与其序列归 newapi_user） ---'
SELECT c.relname,
       c.relkind,
       pg_get_userbyid(c.relowner) AS owner
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relname IN ('log_refunds', 'log_refunds_id_seq',
                    'checkins', 'checkins_id_seq',
                    'users', 'user_subscriptions', 'redemptions', 'logs')
ORDER BY c.relname;

\echo '--- A2. 表属主分布（预期：user_DGrxGm 占绝大多数，newapi_user 只有 log_refunds） ---'
SELECT pg_get_userbyid(c.relowner) AS owner, count(*) AS tables
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'r'
GROUP BY 1
ORDER BY 2 DESC;

\echo '--- A3. log_refunds 的索引/约束（预期：log_id 上有两个唯一对象，名字只差一个 s） ---'
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public' AND tablename = 'log_refunds'
ORDER BY indexname;

\echo '--- A4. 显式 GRANT 记录（owner 之外被授权的角色） ---'
SELECT grantee, privilege_type
FROM information_schema.role_table_grants
WHERE table_schema = 'public' AND table_name = 'log_refunds'
ORDER BY grantee, privilege_type;

\echo '--- A5. 序列是否落后于 max(id)（落后会导致插入撞主键，比权限更隐蔽） ---'
SELECT last_value AS seq_last FROM public.log_refunds_id_seq;
SELECT COALESCE(max(id), 0) AS max_id FROM public.log_refunds;

\echo ''
\echo '================ B. 有效权限（必须用运行角色执行） ================'

\echo '--- B1. has_* 会计入 owner / 角色继承 / PUBLIC 授权，比 A4 更接近真实 ---'
SELECT current_user AS runtime_role,
       pg_get_userbyid(c.relowner) AS table_owner,
       has_table_privilege(current_user, 'public.log_refunds', 'SELECT')            AS can_select,
       has_table_privilege(current_user, 'public.log_refunds', 'INSERT')            AS can_insert,
       has_table_privilege(current_user, 'public.log_refunds', 'UPDATE')            AS can_update,
       has_table_privilege(current_user, 'public.log_refunds', 'DELETE')            AS can_delete,
       has_sequence_privilege(current_user, 'public.log_refunds_id_seq', 'USAGE')   AS can_seq_usage,
       has_sequence_privilege(current_user, 'public.log_refunds_id_seq', 'SELECT')  AS can_seq_select
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relname = 'log_refunds';

\echo '--- B2. 签到表与钱包表（同一批自研功能） ---'
SELECT has_table_privilege(current_user, 'public.checkins', 'SELECT') AS can_select_checkins,
       has_table_privilege(current_user, 'public.checkins', 'INSERT') AS can_insert_checkins,
       has_table_privilege(current_user, 'public.users', 'UPDATE')    AS can_update_users;
\echo '判读：B1 的 can_select/can_insert/can_update/can_seq_usage 四项全为 t 才算通过。'
\echo '      can_delete = t 也无妨——补回代码里没有任何 DELETE。'

\echo ''
\echo '================ C. 决定性验证：真写一次再回滚 ================'
\echo '（用 log_id = -1 探针，真实 log_id 恒为正，不可能撞车）'

BEGIN;

INSERT INTO public.log_refunds
  (user_id, log_id, funding_source, subscription_id, base_quota, quota,
   refund_date, reason, request_id, created_at)
VALUES (0, -1, 'probe', NULL, 0, 0,
        to_char(now(), 'YYYY-MM-DD'), 'probe', 'probe-v4',
        extract(epoch FROM now())::bigint)
RETURNING id AS probe_id;

UPDATE public.log_refunds SET quota = 0 WHERE log_id = -1;

ROLLBACK;

\echo '--- C3. 确认零残留（应为 0） ---'
SELECT count(*) AS probe_left FROM public.log_refunds WHERE log_id = -1;

\echo ''
\echo '若 C 段报 permission denied for table log_refunds                      → 缺表权限，V-4 FAIL'
\echo '              permission denied for sequence log_refunds_id_seq      → 缺序列 USAGE，V-4 FAIL'
\echo '              duplicate key value violates unique constraint         → 序列落后，见 A5'
\echo '修复（需 owner 或超管执行；只 GRANT，绝不改 owner）：'
\echo '  GRANT SELECT, INSERT, UPDATE ON public.log_refunds TO "<运行角色>";'
\echo '  GRANT USAGE, SELECT ON SEQUENCE public.log_refunds_id_seq TO "<运行角色>";'
