FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/reforger-workshop-api .

FROM alpine:3.20

RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/reforger-workshop-api /app/reforger-workshop-api
COPY static /app/static

USER app
EXPOSE 8000
ENTRYPOINT ["/app/reforger-workshop-api"]
