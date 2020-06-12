# InBalance

InBalance is a simple to use, effective and reliable Load Balancer

It uses RoundRobin algorithm to send requests into set of backends and support
retries too.

It also performs active cleaning and passive recovery for unhealthy backends.

# Configuration

To configure, simple modify the config.json file

```json5
{
  "Port": 3030,
  "Backends": [
    "http://127.0.0.1:80"
  ]
}
```

- `Backends` is a list of backends supported being load balanced.

Example:

To add followings as load balanced backends and serve on port 8080
- http://localhost:3031
- http://localhost:3032
- http://localhost:3033
- http://localhost:3034


```json5
{
  "Port": 3030,
  "Backends": [
    "http://localhost:3031",
    "http://localhost:3032",
    "http://localhost:3033",
    "http://localhost:3034"
  ]
}
```
