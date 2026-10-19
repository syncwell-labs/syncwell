FROM golang:1.23-alpine AS build
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X syncwell/internal/version.Version=${VERSION} -X syncwell/internal/version.Commit=${COMMIT} -X syncwell/internal/version.Date=${BUILD_DATE}" -o /out/syncwell ./cmd/syncwell

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
COPY --chown=nonroot:nonroot sdk /sdk
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/syncwell", "-addr", ":8080", "-static", "/demo", "-sdk", "/sdk"]
