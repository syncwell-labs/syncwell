# --- build stage ---
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# Static, stripped, single binary.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/syncwell ./cmd/syncwell

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/syncwell /syncwell
COPY --from=build /src/demo /demo
COPY --from=build /src/sdk /sdk
EXPOSE 8080
ENTRYPOINT ["/syncwell", "-addr", ":8080", "-static", "/demo"]
