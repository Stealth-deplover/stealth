-- name: ListAccountOrganizations :many
SELECT o.id, o.name, o.slug, o.created_at
FROM organizations AS o
JOIN organization_memberships AS m ON m.organization_id = o.id
WHERE m.account_id = $1
  AND ($2::text = '' OR o.id::text > $2::text)
ORDER BY o.id
LIMIT $3;

-- name: ListOrganizationMemberships :many
SELECT m.organization_id, m.account_id, a.email, m.role, m.created_at
FROM organization_memberships AS m
JOIN accounts AS a ON a.id = m.account_id
WHERE m.organization_id = $1
  AND ($2::text = '' OR m.account_id::text > $2::text)
ORDER BY m.account_id
LIMIT $3;
