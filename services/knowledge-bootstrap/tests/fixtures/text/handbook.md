# Engineering

Production deployments require two approvers.

## Deployment

- Verify the image tag.
- Check the health endpoint.

### Commands

```bash
curl http://service/healthz

# This is part of the code, not a document heading.
```

## Rollback

Deploy the previous image.
