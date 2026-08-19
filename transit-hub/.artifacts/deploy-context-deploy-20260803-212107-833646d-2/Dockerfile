FROM deviseo/transithub:v0.1.15

RUN rm -rf /app/public/*
COPY transithub-api /app/transithub-api
COPY public /app/public
RUN chmod +x /app/transithub-api
