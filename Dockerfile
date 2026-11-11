FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/api ./cmd/api \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/historyeval ./cmd/historyeval

FROM alpine:3.22

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /out/api /app/api
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/historyeval /app/historyeval
COPY --from=builder /src/migrations/*.sql /app/migrations/

EXPOSE 8080

CMD ["/app/api"]
