FROM golang:1.25-alpine AS build
RUN apk add --no-cache git
ARG INFINID_REF=main
RUN git clone https://github.com/utkjmitch/infinid.git /src \
 && cd /src && git checkout "$INFINID_REF" \
 && CGO_ENABLED=0 go build -o /infinid ./cmd/infinid

FROM alpine:3.22
COPY --from=build /infinid /infinid
COPY run.sh /run.sh
RUN chmod +x /run.sh
CMD ["/run.sh"]
