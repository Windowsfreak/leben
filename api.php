<?php
header('Content-Type: application/json');
require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? '';
$lang = $_GET['lang'] ?? 'de';
$supported = array_keys(get_supported_languages());
$lang = in_array(strtolower($lang), $supported) ? strtolower($lang) : 'de';

try {
    switch ($action) {
        case 'search':
            $q = $_GET['q'] ?? '';
            $offset = isset($_GET['offset']) ? (int)$_GET['offset'] : 0;
            $limit = isset($_GET['limit']) ? (int)$_GET['limit'] : 20;
            
            // Clean up query
            $q = trim($q);
            
            $results = search_tiles($lang, $q, $offset, $limit);
            
            echo json_encode([
                'status' => 'success',
                'query' => $q,
                'lang' => $lang,
                'count' => count($results),
                'data' => $results
            ]);
            break;
            
        case 'similar':
            $name = $_GET['name'] ?? '';
            $limit = isset($_GET['limit']) ? (int)$_GET['limit'] : 3;
            
            if (empty($name)) {
                throw new Exception("Tile name is required for similarity searches.");
            }
            
            $results = get_similar_tiles($name, $lang, $limit);
            
            echo json_encode([
                'status' => 'success',
                'name' => $name,
                'lang' => $lang,
                'data' => $results
            ]);
            break;
            
        case 'get_tile':
            $name = strtolower(trim($_GET['name'] ?? ''));
            if (empty($name)) {
                throw new Exception("Tile name is required.");
            }
            
            $show_invisible = false;
            if (session_status() === PHP_SESSION_NONE) {
                session_start();
            }
            if (!empty($_SESSION['admin_logged_in'])) {
                $show_invisible = true;
            }

            $ref_codes = get_request_reference_codes();
            $ref_codes_str = implode(',', $ref_codes);

            $db = get_db_connection();
            $stmt = $db->prepare("
                SELECT * FROM tiles 
                WHERE name = :name AND language = :lang 
                  AND (:show_invisible = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array(:ref_codes, ',')))))
                LIMIT 1
            ");
            $stmt->bindValue(':name', $name, PDO::PARAM_STR);
            $stmt->bindValue(':lang', $lang, PDO::PARAM_STR);
            $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
            $stmt->bindValue(':ref_codes', $ref_codes_str, PDO::PARAM_STR);
            $stmt->execute();
            $tile = $stmt->fetch();
            
            if (!$tile) {
                // Try fallback language
                $fallback = ($lang === 'de') ? 'en' : 'de';
                $stmt->bindValue(':lang', $fallback, PDO::PARAM_STR);
                $stmt->execute();
                $tile = $stmt->fetch();
            }
            
            if (!$tile) {
                throw new Exception("Tile '{$name}' not found.");
            }
            
            if (!$show_invisible) {
                unset($tile['secret']);
            }

            echo json_encode([
                'status' => 'success',
                'data' => $tile
            ]);
            break;
            
        case 'get_tile_info':
            $name = strtolower(trim($_GET['name'] ?? ''));
            if (empty($name)) {
                throw new Exception("Tile name is required.");
            }

            $show_invisible = false;
            if (session_status() === PHP_SESSION_NONE) {
                session_start();
            }
            if (!empty($_SESSION['admin_logged_in'])) {
                $show_invisible = true;
            }

            $ref_codes = get_request_reference_codes();
            $ref_codes_str = implode(',', $ref_codes);

            $db = get_db_connection();
            $stmt = $db->prepare("
                SELECT id, name, language, title, created_at, updated_at, visible
                FROM tiles
                WHERE name = :name
                  AND (:show_invisible = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array(:ref_codes, ',')))))
                ORDER BY created_at ASC
            ");
            $stmt->bindValue(':name', $name, PDO::PARAM_STR);
            $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
            $stmt->bindValue(':ref_codes', $ref_codes_str, PDO::PARAM_STR);
            $stmt->execute();
            $versions = $stmt->fetchAll();

            echo json_encode([
                'status' => 'success',
                'name' => $name,
                'versions' => $versions
            ]);
            break;

        default:
            throw new Exception("Invalid API action.");
    }
} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'status' => 'error',
        'message' => $e->getMessage()
    ]);
}
