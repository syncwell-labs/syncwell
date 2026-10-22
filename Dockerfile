FROM golang:1.26-alpine AS build
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
COPY --chown=nonroot:nonroot examples/demos /demo
COPY --chown=nonroot:nonroot clients/javascript/syncwell.js clients/javascript/crdt.js clients/javascript/sequence.js clients/javascript/text.js clients/javascript/awareness.js clients/javascript/storage.js /sdk/
EXPOSE 8080
USER nonroot:nonroot
STOPSIGNAL SIGTERM
ENTRYPOINT ["/syncwell", "-static", "/demo", "-sdk", "/sdk"]
