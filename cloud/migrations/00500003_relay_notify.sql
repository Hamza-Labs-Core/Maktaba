-- +goose Up
-- Story 50.05: the relay caches each home's lookup and drops an entry the
-- moment it changes. Triggers send the notification for every path that
-- changes a home (a plan from the billing webhook, a slug, an owner's
-- suspension, an unlink), so no handler can forget it.
--   tier_changed   payload: the slug whose plan changed
--   server_changed payload: the slug that changed, was removed or whose owner changed status

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION relay_notify_server() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('server_changed', OLD.slug);
        RETURN OLD;
    END IF;
    IF NEW.plan IS DISTINCT FROM OLD.plan THEN
        PERFORM pg_notify('tier_changed', NEW.slug);
    END IF;
    IF NEW.slug IS DISTINCT FROM OLD.slug OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id
       OR NEW.server_secret_hash IS DISTINCT FROM OLD.server_secret_hash THEN
        PERFORM pg_notify('server_changed', OLD.slug);
        IF NEW.slug IS DISTINCT FROM OLD.slug THEN
            PERFORM pg_notify('server_changed', NEW.slug);
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION relay_notify_user() RETURNS trigger AS $$
DECLARE
    s TEXT;
BEGIN
    FOR s IN SELECT slug FROM servers WHERE owner_user_id = NEW.id LOOP
        IF NEW.plan IS DISTINCT FROM OLD.plan THEN
            PERFORM pg_notify('tier_changed', s);
        END IF;
        IF NEW.status IS DISTINCT FROM OLD.status THEN
            PERFORM pg_notify('server_changed', s);
        END IF;
    END LOOP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS relay_notify_server ON servers;
CREATE TRIGGER relay_notify_server AFTER UPDATE OR DELETE ON servers
    FOR EACH ROW EXECUTE FUNCTION relay_notify_server();

DROP TRIGGER IF EXISTS relay_notify_user ON users;
CREATE TRIGGER relay_notify_user AFTER UPDATE OF plan, status ON users
    FOR EACH ROW EXECUTE FUNCTION relay_notify_user();

-- +goose Down
DROP TRIGGER IF EXISTS relay_notify_user ON users;
DROP TRIGGER IF EXISTS relay_notify_server ON servers;
DROP FUNCTION IF EXISTS relay_notify_user();
DROP FUNCTION IF EXISTS relay_notify_server();
