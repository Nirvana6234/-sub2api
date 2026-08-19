FROM transithub:20260810-amount-currency

COPY transithub-api /app/transithub-api
COPY public /app/public
RUN chmod +x /app/transithub-api

ENTRYPOINT ["/app/transithub-api"]
