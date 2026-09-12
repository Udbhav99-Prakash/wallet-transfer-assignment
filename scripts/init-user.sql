-- Create least-privileged application role for the wallet service
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'wallet_app') THEN
        CREATE ROLE wallet_app WITH LOGIN PASSWORD 'wallet_app_password';
        GRANT ALL PRIVILEGES ON DATABASE wallet_db TO wallet_app;
        GRANT ALL ON SCHEMA public TO wallet_app;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO wallet_app;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO wallet_app;
    END IF;
END $$;

