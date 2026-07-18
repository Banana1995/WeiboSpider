FROM python:3.9-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    CHROME_PATH=/usr/bin/chromium

RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium \
    fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

RUN useradd -r -u 1000 app && \
    mkdir -p /app/weibospider/data && \
    chown -R app:app /app

USER app

EXPOSE 5050

CMD ["python", "weibospider/run.py", "--host", "0.0.0.0", "--port", "5050"]
