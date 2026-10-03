FROM golang:1.26.5
RUN apt-get update && apt-get install -y --no-install-recommends postgresql-client-17 && rm -rf /var/lib/apt/lists/*
WORKDIR /src