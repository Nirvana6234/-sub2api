-- 压缩前 token 数：原计划用于更精确的节省比例计算，后因 headroom 自己在响应头阶段
-- 对 OpenAI/Gemini 流量只有粗估值（经常是 0）而放弃——改在查询时用 usage_logs 已有的
-- input_tokens + cache_creation_tokens + cache_read_tokens 反推。这一列目前没有任何
-- 代码读写，保留只是为了与生产的迁移历史保持一致。
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS headroom_tokens_before INTEGER NOT NULL DEFAULT 0;
