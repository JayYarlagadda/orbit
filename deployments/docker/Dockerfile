# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.7
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGET=orbitd
ARG LDFLAGS="-s -w"
RUN CGO_ENABLED=0 go build -trimpath -ldflags "${LDFLAGS}" -o /out/service ./cmd/${TARGET}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /orbit
COPY --from=build /src/migrations /migrations
USER nonroot:nonroot
ENTRYPOINT ["/orbit"]
