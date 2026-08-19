FROM transithub:20260814-schedulability-recovery-r1

COPY transithub-api /app/transithub-api
COPY public /app/public

RUN chmod 0755 /app/transithub-api

ENTRYPOINT ["/app/transithub-api"]
