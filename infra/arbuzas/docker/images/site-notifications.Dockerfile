FROM python:3.11-slim

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /opt/site-notifications

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

COPY workloads/site-notifications/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

COPY workloads/site-notifications/app.py ./app.py
COPY workloads/site-notifications/config.py ./config.py
COPY workloads/site-notifications/env_store.py ./env_store.py
COPY workloads/site-notifications/gribu_auth.py ./gribu_auth.py
COPY workloads/site-notifications/gribu_client.py ./gribu_client.py
COPY workloads/site-notifications/process_lock.py ./process_lock.py
COPY workloads/site-notifications/scheduler.py ./scheduler.py
COPY workloads/site-notifications/state_store.py ./state_store.py
COPY workloads/site-notifications/telegram_control.py ./telegram_control.py
COPY workloads/site-notifications/unread_parser.py ./unread_parser.py

CMD ["python", "/opt/site-notifications/app.py", "daemon"]

