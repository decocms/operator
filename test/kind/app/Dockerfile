FROM denoland/deno:alpine-2.1.4

WORKDIR /app

# Copy application
COPY main.ts .

# Create parent directory for mounted file
RUN mkdir -p /app

# Expose port
EXPOSE 8080

# Run the application
CMD ["run", "--allow-net", "--allow-read=/app", "--allow-env", "main.ts"]

