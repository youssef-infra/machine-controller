FROM golang:1.25 AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY pkg/ pkg/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -o machine-controller ./cmd/machine-controller/

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /app/machine-controller /machine-controller
ENTRYPOINT ["/machine-controller"]
