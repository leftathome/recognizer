FROM python:3.12-slim

WORKDIR /app

COPY images/notification-relay/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY images/notification-relay/relay/ relay/
COPY schemas/ /schemas/

ENV CAPTURE_SCHEMA_DIR=/schemas

EXPOSE 8080

USER 1000:1000

CMD ["python", "-m", "relay.main"]
