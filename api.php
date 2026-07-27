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
            
            $db = get_db_connection();
            $stmt = $db->prepare("
                SELECT * FROM tiles 
                WHERE name = :name AND language = :lang 
                LIMIT 1
            ");
            $stmt->execute([':name' => $name, ':lang' => $lang]);
            $tile = $stmt->fetch();
            
            if (!$tile) {
                // Try fallback language
                $fallback = ($lang === 'de') ? 'en' : 'de';
                $stmt->execute([':name' => $name, ':lang' => $fallback]);
                $tile = $stmt->fetch();
            }
            
            if (!$tile) {
                throw new Exception("Tile '{$name}' not found.");
            }
            
            echo json_encode([
                'status' => 'success',
                'data' => $tile
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
