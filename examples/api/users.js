/**
 * Create query to pass to request database handler
 *
 * @param {{pathParams: any, queryParams: any}} ctx contains the request path parameters and query string parameters.
 * @returns {any[]} sql query with query values eg `["SELECT * FROM users WHERE id = ?", 123]`
 */
function sql(ctx) {
  return ["SELECT * FROM users"];
}
