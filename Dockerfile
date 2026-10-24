FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X backend/internal/version.Version=${VERSION}" \
    -o /out/nivwest-api \
    ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/nivwest-api /nivwest-api

USER nonroot:nonroot
EXPOSE 8081

ENTRYPOINT ["/nivwest-api"]