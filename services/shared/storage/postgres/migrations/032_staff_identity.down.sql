-- 032_staff_identity.down.sql

SET search_path TO axiaops;

-- Grants first (FK → staff_users); CASCADE is belt-and-braces.
DROP TABLE IF EXISTS staff_role_grants CASCADE;
DROP TABLE IF EXISTS staff_users CASCADE;
