FROM golang:1.23-alpine AS build
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X syncwell/internal/version.Version=${VERSION} -X syncwell/internal/version.Commit=${COMMIT} -X syncwell/internal/version.Date=${BUILD_DATE}" -o /out/syncwell ./cmd/syncwell

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="Syncwell"
LABEL org.opencontainers.image.description="Self-hostable real-time collaboration engine"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${COMMIT}"
LABEL org.opencontainers.image.created="${BUILD_DATE}"
COPY --from=build --chown=nonroot:nonroot /out/syncwell /syncwell
COPY --chown=nonroot:nonroot demo /demo
COPY --chown=nonroot:nonroot sdk/syncwell.js sdk/crdt.js sdk/sequence.js sdk/text.js sdk/awareness.js sdk/storage.js /sdk/
EXPOSE 8080
USER nonroot:nonroot
STOPSIGNAL SIGTERM
ENTRYPOINT ["/syncwell", "-static", "/demo", "-sdk", "/sdk"]
