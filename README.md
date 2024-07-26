# Copycat
A simple pastebin-like website, where users may anonymously upload plaintext and file attachments up to 35 MiB in total, 
and share the shortened links with others. The upload plaintexts are stored on an Amazon RDS PostgreSQL database along
with the file attachment hashes which act as keys to download the attachments from the S3 service. Attachment downloads 
and uploads are performed in parallel using the Amazon Web Services SDK for Go. Front-facing HTML pages are generated 
using the html/template package in the Go standard library. Web requests and routing are performed using the Gin web
framework for Go. The webserver is hosted on AWS Elastic Beanstalk, which handles load balancing and scaling, and
provisioning EC2 virtual machines.

# Configuring AWS Credentials
https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/

Create a .aws/credentials file in your user home directory:

```
[default]
aws_access_key_id=<YOUR_ACCESS_KEY_ID>
aws_secret_access_key=<YOUR_SECRET_ACCESS_KEY>
```

# Assigning .env Variables
The server uses several secret environment variables stored in a file that is excluded from Git named .env:

```sh
BASEURL="example.com" or "localhost:8080" for example
PORT=8080
AWS_REGION="us-east-1"
S3_BUCKET="Your S3 bucket name"
DB_HOST="Your PostgreSQL database IP"
DB_PORT="5432"
DB_USER="postgres"
DB_PASS="Your PostgreSQL database password"
```
