# GPT-5.6 纯度检测器旁路服务。
#
# 检测器本身零第三方 Python 依赖（全部 stdlib），所以不需要 pip install。
# 但 native_codex 请求格式由一个 .mjs 通过 Node 原始 socket 发出
# （vendor/gpt56_vnext/transport.py 里 shutil.which("node")），高档检测会用到，
# 所以镜像里必须有 node。那个 .mjs 只 import node: 内置模块，不需要 npm install。
FROM python:3.13-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY vendor/ /app/vendor/
COPY serve.py /app/serve.py

# 构建期就把语法问题暴露出来，而不是等运行时第一个请求才炸。
RUN python -m compileall -q /app/vendor/gpt56_vnext \
    && node --input-type=module -e "process.exit(0)"

ENV GPT56_BIND_HOST=0.0.0.0 \
    GPT56_BIND_PORT=8760 \
    GPT56_RUNS_ROOT=/data/runs \
    PYTHONUNBUFFERED=1

# 只 EXPOSE，不在 compose 里做 ports 映射：这个服务没有业务鉴权，
# 只能待在容器内网，绝不能暴露到公网。
EXPOSE 8760

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8760/api/health',timeout=3).status==200 else 1)"

CMD ["python", "/app/serve.py"]
