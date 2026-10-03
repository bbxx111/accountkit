FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags="-s -w" -o /out/accountsvc ./cmd/accountsvc

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/accountsvc /accountsvc
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/accountsvc"]
CMD ["serve"]
