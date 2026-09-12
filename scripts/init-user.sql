-- Provision least-privileged application role for local development/Docker
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'wallet_app') THEN
        CREATE ROLE wallet_app WITH LOGIN PASSWORD 'wallet_app_password';
    END IF;
END $$;

-- Create test database if not exists
SELECT 'CREATE DATABASE wallet_test_db OWNER postgres'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'wallet_test_db')\gexec

-- Grant connection, schema DDL, and object privileges for migration execution and runtime access
GRANT CONNECT ON DATABASE wallet_db TO wallet_app;
GRANT CONNECT ON DATABASE wallet_test_db TO wallet_app;

\c wallet_db
GRANT ALL ON SCHEMA public TO wallet_app;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO wallet_app;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO wallet_app;
GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO wallet_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO wallet_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO wallet_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON FUNCTIONS TO wallet_app;

\c wallet_test_db
GRANT ALL ON SCHEMA public TO wallet_app;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO wallet_app;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO wallet_app;
GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO wallet_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO wallet_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO wallet_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON FUNCTIONS TO wallet_app;



