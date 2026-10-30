# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/headscale-exporter .

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="headscale-exporter" \
      org.opencontainers.image.description="Prometheus exporter for Headscale" \
      org.opencontainers.image.source="https://github.com/TuF3i/headscale-exporter" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/headscale-exporter /headscale-exporter

EXPOSE 9110

ENTRYPOINT ["/headscale-exporter"]
