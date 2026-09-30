FROM golang:1.27.1-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/media-intake \
    ./cmd/media-intake

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/media-intake /media-intake

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/media-intake"]
