FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 QUOTES_HOST=0.0.0.0
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt && useradd --uid 1000 --create-home app
COPY app.py ./
USER app
EXPOSE 5053
CMD ["python", "app.py"]
