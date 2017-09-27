FROM alpine:3.6

RUN apk add --no-cache ca-certificates

ADD ./build/sfncli /usr/bin/sfncli
CMD ["bin/sfncli", "--activityname", "${_DEPLOY_ENV}--${_APP_NAME}", "--region", "us-west-2", "--workername", "ECS_TASK_ARN", "--cmd", "echo"]
