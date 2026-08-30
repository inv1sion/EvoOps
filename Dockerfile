FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/evoops ./cmd/evoops

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/evoops /app/evoops
COPY data/demo /app/data/demo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/evoops", "serve"]

