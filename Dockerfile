FROM --platform=$BUILDPLATFORM golang:1.26.5 AS builder

ARG TARGETARCH
ARG TARGETOS=linux

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a \
    -ldflags="-s -w" -o aistiod ./cmd/aistiod
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a \
    -ldflags="-s -w" -o aistioctl ./cmd/aistioctl

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/aistiod .
COPY --from=builder /workspace/aistioctl .
USER 65532:65532
ENTRYPOINT ["/aistiod"]
