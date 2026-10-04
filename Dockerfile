FROM golang:1.27.1-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/media-intake ./cmd/media-intake
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/downstream-stub ./cmd/downstream-stub
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck
RUN mkdir -p /out/data/captures && chown -R 65532:65532 /out/data

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/healthcheck /healthcheck
USER 65532:65532
EXPOSE 8080

FROM runtime AS downstream
COPY --from=build /out/downstream-stub /downstream-stub
ENTRYPOINT ["/downstream-stub"]

FROM runtime AS app
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/media-intake /media-intake
ENTRYPOINT ["/media-intake"]
