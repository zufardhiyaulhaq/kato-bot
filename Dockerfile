FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/kato-bot ./cmd/kato-bot

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/kato-bot /kato-bot
USER nonroot:nonroot
ENTRYPOINT ["/kato-bot"]
